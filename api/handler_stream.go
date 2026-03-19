package api

import (
	"bufio"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"pansou/config"
	"pansou/model"
	"pansou/util"
	jsonutil "pansou/util/json"
)

// StreamSearchHandler 流式搜索处理函数：每收到一批结果即推送，使用 NDJSON 格式
func StreamSearchHandler(c *gin.Context) {
	req, ext, err := parseSearchRequest(c)
	if err != nil {
		return
	}

	// 设置流式响应头
	c.Header("Content-Type", "application/x-ndjson; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	writer := bufio.NewWriter(c.Writer)
	flush := func() {
		writer.Flush()
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	err = searchService.SearchStream(
		req.Keyword, req.Channels, req.Concurrency, req.ForceRefresh,
		req.ResultType, req.SourceType, req.Plugins, req.CloudTypes, ext,
		func(resp model.SearchResponse) error {
			// 应用过滤器
			if req.Filter != nil {
				resp = applyResultFilter(resp, req.Filter, req.ResultType)
			}
			line := model.NewSuccessResponse(resp)
			data, err := jsonutil.Marshal(line)
			if err != nil {
				return err
			}
			writer.Write(data)
			writer.WriteByte('\n')
			flush()
			return nil
		},
	)

	if err != nil {
		errResp := model.NewErrorResponse(500, "流式搜索失败: "+err.Error())
		jsonData, _ := jsonutil.Marshal(errResp)
		writer.Write(jsonData)
		writer.WriteByte('\n')
		flush()
	}
}

// parseSearchRequest 解析搜索请求参数，供 SearchHandler 和 StreamSearchHandler 共用
func parseSearchRequest(c *gin.Context) (model.SearchRequest, map[string]interface{}, error) {
	var req model.SearchRequest
	if c.Request.Method == http.MethodGet {
		keyword := c.Query("kw")
		channelsStr := c.Query("channels")
		var channels []string
		if channelsStr != "" && channelsStr != " " {
			for _, part := range strings.Split(channelsStr, ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					channels = append(channels, trimmed)
				}
			}
		}
		concurrency := 0
		if concStr := c.Query("conc"); concStr != "" && concStr != " " {
			concurrency = util.StringToInt(concStr)
		}
		forceRefresh := c.Query("refresh") == "true" || c.Query("refresh") == "1"
		resultType := c.Query("res")
		if resultType == "" || resultType == " " {
			resultType = "merge"
		}
		sourceType := c.Query("src")
		if sourceType == "" || sourceType == " " {
			sourceType = "all"
		}
		var plugins []string
		if c.Request.URL.Query().Has("plugins") {
			if pluginsStr := c.Query("plugins"); pluginsStr != "" && pluginsStr != " " {
				for _, part := range strings.Split(pluginsStr, ",") {
					if trimmed := strings.TrimSpace(part); trimmed != "" {
						plugins = append(plugins, trimmed)
					}
				}
			}
		}
		var cloudTypes []string
		if c.Request.URL.Query().Has("cloud_types") {
			if ctStr := c.Query("cloud_types"); ctStr != "" && ctStr != " " {
				for _, part := range strings.Split(ctStr, ",") {
					if trimmed := strings.TrimSpace(part); trimmed != "" {
						cloudTypes = append(cloudTypes, trimmed)
					}
				}
			}
		}
		var ext map[string]interface{}
		if extStr := c.Query("ext"); extStr != "" && extStr != " " {
			if extStr == "{}" {
				ext = make(map[string]interface{})
			} else if err := jsonutil.Unmarshal([]byte(extStr), &ext); err != nil {
				c.JSON(http.StatusBadRequest, model.NewErrorResponse(400, "无效的ext参数格式: "+err.Error()))
				return req, nil, err
			}
		}
		if ext == nil {
			ext = make(map[string]interface{})
		}
		var filter *model.FilterConfig
		if filterStr := c.Query("filter"); filterStr != "" && filterStr != " " {
			filter = &model.FilterConfig{}
			if err := jsonutil.Unmarshal([]byte(filterStr), filter); err != nil {
				c.JSON(http.StatusBadRequest, model.NewErrorResponse(400, "无效的filter参数格式: "+err.Error()))
				return req, nil, err
			}
		}
		waitFull := c.Query("wait_full") == "true" || c.Query("wait_full") == "1"
		req = model.SearchRequest{
			Keyword: keyword, Channels: channels, Concurrency: concurrency,
			ForceRefresh: forceRefresh, ResultType: resultType, SourceType: sourceType,
			Plugins: plugins, CloudTypes: cloudTypes, Ext: ext, Filter: filter, WaitFull: waitFull,
		}
	} else {
		data, err := c.GetRawData()
		if err != nil {
			c.JSON(http.StatusBadRequest, model.NewErrorResponse(400, "读取请求数据失败: "+err.Error()))
			return req, nil, err
		}
		if err := jsonutil.Unmarshal(data, &req); err != nil {
			c.JSON(http.StatusBadRequest, model.NewErrorResponse(400, "无效的请求参数: "+err.Error()))
			return req, nil, err
		}
	}

	if len(req.Channels) == 0 {
		req.Channels = config.AppConfig.DefaultChannels
	}
	if req.ResultType == "" {
		req.ResultType = "merged_by_type"
	} else if req.ResultType == "merge" {
		req.ResultType = "merged_by_type"
	}
	if req.SourceType == "" {
		req.SourceType = "all"
	}
	if req.SourceType == "tg" {
		req.Plugins = nil
	} else if req.SourceType == "plugin" {
		req.Channels = nil
	} else if req.SourceType == "all" && len(req.Plugins) == 0 {
		req.Plugins = nil
	}

	ext := req.Ext
	if ext == nil {
		ext = make(map[string]interface{})
	}
	if req.WaitFull {
		ext["_wait_full"] = true
	}
	return req, ext, nil
}
