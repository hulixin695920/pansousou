package service

import (
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"pansou/config"
	"pansou/model"
	"pansou/plugin"
	"pansou/util/cache"
)

// SearchStream 流式搜索：每收到一批结果就通过 send 回调推送，支持增量返回
func (s *SearchService) SearchStream(
	keyword string, channels []string, concurrency int, forceRefresh bool, resultType string, sourceType string,
	plugins []string, cloudTypes []string, ext map[string]interface{},
	send func(model.SearchResponse) error,
) error {
	if ext == nil {
		ext = make(map[string]interface{})
	}

	// 参数预处理（与 Search 一致）
	if sourceType == "" {
		sourceType = "all"
	}
	if sourceType == "tg" {
		plugins = nil
	} else if sourceType == "all" || sourceType == "plugin" {
		if len(plugins) == 0 {
			plugins = nil
		} else {
			hasNonEmpty := false
			for _, p := range plugins {
				if p != "" {
					hasNonEmpty = true
					break
				}
			}
			if !hasNonEmpty {
				plugins = nil
			} else if s.pluginManager != nil {
				allPlugins := s.pluginManager.GetPlugins()
				allPluginNames := make([]string, 0, len(allPlugins))
				for _, p := range allPlugins {
					allPluginNames = append(allPluginNames, strings.ToLower(p.Name()))
				}
				requestedPlugins := make([]string, 0, len(plugins))
				for _, p := range plugins {
					if p != "" {
						requestedPlugins = append(requestedPlugins, strings.ToLower(p))
					}
				}
				if len(requestedPlugins) == len(allPluginNames) {
					pluginMap := make(map[string]bool)
					for _, p := range requestedPlugins {
						pluginMap[p] = true
					}
					allIncluded := true
					for _, name := range allPluginNames {
						if !pluginMap[name] {
							allIncluded = false
							break
						}
					}
					if allIncluded {
						plugins = nil
					}
				}
			}
		}
	}
	if concurrency <= 0 {
		concurrency = config.AppConfig.DefaultConcurrency
	}

	// 获取可用插件列表
	var availablePlugins []plugin.AsyncSearchPlugin
	if (sourceType == "all" || sourceType == "plugin") && config.AppConfig.AsyncPluginEnabled && s.pluginManager != nil {
		allPlugins := s.pluginManager.GetPlugins()
		hasPlugins := len(plugins) > 0
		hasNonEmptyPlugin := false
		if hasPlugins {
			for _, p := range plugins {
				if p != "" {
					hasNonEmptyPlugin = true
					break
				}
			}
		}
		if hasPlugins && hasNonEmptyPlugin {
			pluginMap := make(map[string]bool)
			for _, p := range plugins {
				if p != "" {
					pluginMap[strings.ToLower(p)] = true
				}
			}
			for _, p := range allPlugins {
				if pluginMap[strings.ToLower(p.Name())] {
					availablePlugins = append(availablePlugins, p)
				}
			}
		} else {
			availablePlugins = allPlugins
		}
	}

	// 计算数据源数量
	totalSources := 0
	if sourceType == "all" || sourceType == "tg" {
		totalSources++
	}
	totalSources += len(availablePlugins)

	if totalSources == 0 {
		return send(buildStreamResponse(nil, keyword, resultType, cloudTypes))
	}

	type streamBatch struct {
		isTG    bool
		results []model.SearchResult
	}

	resultChan := make(chan streamBatch, totalSources+5)
	var wg sync.WaitGroup

	// TG 搜索
	if sourceType == "all" || sourceType == "tg" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, _ := s.searchTG(keyword, channels, forceRefresh)
			resultChan <- streamBatch{isTG: true, results: results}
		}()
	}

	// 插件搜索：每个插件独立 goroutine，完成后发送到 channel
	if forceRefresh {
		ext["refresh"] = true
	}
	cacheKey := cache.GeneratePluginCacheKey(keyword, plugins)

	// 流式模式跳过缓存，直接搜索
	for _, p := range availablePlugins {
		wg.Add(1)
		plugin := p
		go func() {
			defer wg.Done()
			plugin.SetMainCacheKey(cacheKey)
			plugin.SetCurrentKeyword(keyword)
			results, err := plugin.AsyncSearch(keyword, func(client *http.Client, kw string, extParams map[string]interface{}) ([]model.SearchResult, error) {
				return plugin.Search(kw, extParams)
			}, cacheKey, ext)
			if err != nil {
				resultChan <- streamBatch{isTG: false, results: nil}
				return
			}
			// 只保留有链接的结果
			filtered := make([]model.SearchResult, 0, len(results))
			for _, r := range results {
				if len(r.Links) > 0 {
					filtered = append(filtered, r)
				}
			}
			resultChan <- streamBatch{isTG: false, results: filtered}
		}()
	}

	// 收集并流式推送
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	streamStart := time.Now()
	// 方案2：后台继续更新缓存后，在同一个连接里继续推送（轮询插件主缓存）
	streamMaxWait := config.AppConfig.PluginTimeout
	if streamMaxWait <= 0 {
		streamMaxWait = 30 * time.Second
	}
	pollInterval := 1 * time.Second

	var tgResults []model.SearchResult
	var pluginResults []model.SearchResult // 初始阶段从 AsyncSearch 返回；后续轮询会以主缓存为准

	lastSentHash := ""

	sendIfChanged := func(allResults []model.SearchResult) error {
		sortResultsByTimeAndKeywords(allResults)
		h := hashSearchResults(allResults)
		if h == lastSentHash {
			return nil
		}
		lastSentHash = h
		resp := buildStreamResponse(allResults, keyword, resultType, cloudTypes)
		return send(resp)
	}

	// 初始阶段：按 goroutine 返回的批次推送（4 秒快速路径通常先出部分）
	for batch := range resultChan {
		if len(batch.results) == 0 {
			continue
		}

		if batch.isTG {
			tgResults = mergeSearchResults(tgResults, batch.results)
		} else {
			pluginResults = mergeSearchResults(pluginResults, batch.results)
		}

		allResults := mergeSearchResults(tgResults, pluginResults)
		if err := sendIfChanged(allResults); err != nil {
			return err
		}
	}

	// 后台持续阶段：轮询插件主缓存，直到超时或结果不再变化
	// 注意：只在包含插件搜索的场景下轮询。
	shouldPollPlugins := sourceType == "all" || sourceType == "plugin"
	if !shouldPollPlugins {
		return nil
	}
	if !cacheInitialized || !config.AppConfig.CacheEnabled || enhancedTwoLevelCache == nil {
		return nil
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// 只有在首次成功读到缓存之后，才开始计数“稳定次数”，避免还没等到后台写入就提前退出。
	gotCache := false
	stableCount := 0
	lastCacheHash := ""

	for {
		if time.Since(streamStart) >= streamMaxWait {
			return nil
		}

		select {
		case <-ticker.C:
			// 读取插件主缓存的全量（可能从部分 → 最终逐步增长）
			data, hit, err := enhancedTwoLevelCache.Get(cacheKey)
			if err != nil || !hit || len(data) == 0 {
				continue
			}

			var cachedPluginResults []model.SearchResult
			if err := enhancedTwoLevelCache.GetSerializer().Deserialize(data, &cachedPluginResults); err != nil {
				continue
			}

			// 仍然只保留有链接的结果（和主逻辑一致）
			filtered := make([]model.SearchResult, 0, len(cachedPluginResults))
			for _, r := range cachedPluginResults {
				if len(r.Links) > 0 {
					filtered = append(filtered, r)
				}
			}

			cacheMerged := mergeSearchResults(tgResults, filtered)

			// 用合并后的结果做 hash，避免前端重复刷
			cacheHash := hashSearchResults(cacheMerged)
			if !gotCache {
				gotCache = true
				lastCacheHash = cacheHash
				stableCount = 0

				if err := sendIfChanged(cacheMerged); err != nil {
					return err
				}
				continue
			}

			if cacheHash == lastCacheHash {
				stableCount++
				if stableCount >= 3 {
					return nil
				}
				continue
			}

			stableCount = 0
			lastCacheHash = cacheHash

			if err := sendIfChanged(cacheMerged); err != nil {
				return err
			}
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// buildStreamResponse 构建流式响应（与 Search 的响应格式一致）
func buildStreamResponse(allResults []model.SearchResult, keyword string, resultType string, cloudTypes []string) model.SearchResponse {
	filteredForResults := make([]model.SearchResult, 0, len(allResults))
	for _, result := range allResults {
		source := getResultSource(result)
		pluginLevel := getPluginLevelBySource(source)
		if !result.Datetime.IsZero() || getKeywordPriority(result.Title) > 0 || pluginLevel <= 2 {
			filteredForResults = append(filteredForResults, result)
		}
	}
	mergedLinks := mergeResultsByType(allResults, keyword, cloudTypes)
	var total int
	if resultType == "merged_by_type" || resultType == "" {
		total = 0
		for _, links := range mergedLinks {
			total += len(links)
		}
	} else {
		total = len(filteredForResults)
	}
	resp := model.SearchResponse{
		Total:        total,
		Results:      filteredForResults,
		MergedByType: mergedLinks,
	}
	return filterResponseByType(resp, resultType)
}

func hashSearchResults(results []model.SearchResult) string {
	// 轻量 hash：基于 UniqueID + 标题（减少碰撞概率）
	h := md5.New()
	for _, r := range results {
		h.Write([]byte(r.UniqueID))
		h.Write([]byte("|"))
		h.Write([]byte(r.Title))
		h.Write([]byte(";"))
	}
	return hex.EncodeToString(h.Sum(nil))
}
