package service

import (
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

	resultChan := make(chan []model.SearchResult, totalSources+5)
	var wg sync.WaitGroup

	// TG 搜索
	if sourceType == "all" || sourceType == "tg" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, _ := s.searchTG(keyword, channels, forceRefresh)
			resultChan <- results
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
				resultChan <- nil
				return
			}
			// 只保留有链接的结果
			filtered := make([]model.SearchResult, 0, len(results))
			for _, r := range results {
				if len(r.Links) > 0 {
					filtered = append(filtered, r)
				}
			}
			resultChan <- filtered
		}()
	}

	// 收集并流式推送
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	var allResults []model.SearchResult
	for batch := range resultChan {
		if batch != nil && len(batch) > 0 {
			allResults = mergeSearchResults(allResults, batch)
			sortResultsByTimeAndKeywords(allResults)
			resp := buildStreamResponse(allResults, keyword, resultType, cloudTypes)
			if err := send(resp); err != nil {
				return err
			}
		}
	}

	// 最终缓存更新
	if cacheInitialized && config.AppConfig.CacheEnabled && len(allResults) > 0 {
		go func(res []model.SearchResult, kw string, key string) {
			ttl := time.Duration(config.AppConfig.CacheTTLMinutes) * time.Minute
			if enhancedTwoLevelCache != nil {
				data, err := enhancedTwoLevelCache.GetSerializer().Serialize(res)
				if err == nil {
					enhancedTwoLevelCache.SetBothLevels(key, data, ttl)
				}
			}
		}(allResults, keyword, cacheKey)
	}

	return nil
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
