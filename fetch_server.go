package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"

	fastls "github.com/FastTLS/fastls"
	"github.com/FastTLS/fastls/imitate"
	"github.com/gin-gonic/gin"
)

// FetchRequest 请求结构体
type FetchRequest struct {
	URL             string            `json:"url" binding:"required"`
	Method          string            `json:"method"`
	Headers         map[string]string `json:"headers"`
	Body            string            `json:"body"`
	Proxy           string            `json:"proxy"`
	Timeout         int               `json:"timeout"`
	DisableRedirect bool              `json:"disableRedirect"`
	UserAgent       string            `json:"userAgent"`
	Fingerprint     map[string]string `json:"fingerprint"` // {"type": "ja3", "value": "..."} 或 {"type": "ja4r", "value": "..."}
	Browser         string            `json:"browser"`     // 浏览器类型: "chrome", "chrome120", "chrome142", "chromium", "edge", "firefox"(latest), "firefox144", "firefox147", "firefox151", "safari", "opera"
	Cookies         []fastls.Cookie   `json:"cookies"`
}

// FetchResponse 响应结构体
type FetchResponse struct {
	OK      bool              `json:"ok"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Error   string            `json:"error,omitempty"`
}

func main() {
	var (
		addr        string
		debug       bool
		showVersion bool
		showHelp    bool
	)

	// 定义完整参数和缩写参数
	flag.StringVar(&addr, "addr", ":8800", "监听地址 (例如: :8800 或 0.0.0.0:8800)")
	flag.StringVar(&addr, "a", ":8800", "监听地址 (addr 的缩写)")

	flag.BoolVar(&debug, "debug", false, "启用 Debug 模式，显示详细的调试日志")
	flag.BoolVar(&debug, "d", false, "启用 Debug 模式 (debug 的缩写)")

	flag.BoolVar(&showVersion, "version", false, "显示版本信息并退出")
	flag.BoolVar(&showVersion, "v", false, "显示版本信息并退出 (version 的缩写)")

	flag.BoolVar(&showHelp, "help", false, "显示帮助信息并退出")
	flag.BoolVar(&showHelp, "h", false, "显示帮助信息并退出 (help 的缩写)")

	flag.Usage = func() {
		printHelp()
	}

	flag.Parse()

	if showHelp || len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		printHelp()
		os.Exit(0)
	}

	if showVersion {
		printVersion()
		os.Exit(0)
	}

	// 设置最大CPU核心数
	runtime.GOMAXPROCS(runtime.NumCPU())

	// 初始化Fastls客户端（全局单例，支持高并发）
	client := fastls.NewClient()

	// 设置gin模式
	if debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()

	// 添加CORS中间件（如果需要跨域访问）
	r.Use(corsMiddleware())

	// 健康检查端点
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// 主要的fetch端点
	r.POST("/fetch", func(c *gin.Context) {
		handleFetch(c, client)
	})

	// 启动服务器
	log.Printf("Fetch服务启动在端口 %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}

// handleFetch 处理fetch请求
func handleFetch(c *gin.Context, client fastls.Fastls) {
	var req FetchRequest

	// 绑定JSON请求体
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, FetchResponse{
			OK:    false,
			Error: "请求参数错误: " + err.Error(),
		})
		return
	}

	// 设置默认值
	if req.Method == "" {
		req.Method = "GET"
	}
	if req.Timeout == 0 {
		req.Timeout = 30 // 默认30秒超时
	}
	if req.Headers == nil {
		req.Headers = make(map[string]string)
	}

	// 构建Fastls选项
	options := fastls.Options{
		Headers:         make(map[string]string),
		Body:            req.Body,
		Proxy:           req.Proxy,
		Timeout:         req.Timeout,
		DisableRedirect: req.DisableRedirect,
		Cookies:         req.Cookies,
	}

	// 解析请求头中的指纹配置（优先级最高）
	requestConfig := parseFingerprintFromHeaders(c.Request.Header)
	headerProxy, _ := parseProxyFromHeaders(c.Request.Header)

	// 先复制用户的请求头到options（排除 X-Mitm-* 头）
	for key, value := range req.Headers {
		if !strings.HasPrefix(strings.ToLower(key), "x-mitm-") {
			options.Headers[key] = value
		}
	}

	// 优先使用请求头中的代理信息
	if headerProxy != nil {
		options.Proxy = headerProxy.String()
	} else if req.Proxy != "" {
		options.Proxy = req.Proxy
	}

	// 优先使用请求头中的指纹配置
	if requestConfig != nil && requestConfig.Override {
		applyImitateConfig(&options, requestConfig)

		// 优先使用请求头中的 HTTP2SettingsString，如果存在则覆盖
		if requestConfig.HTTP2SettingsString != "" {
			options.HTTP2SettingsString = requestConfig.HTTP2SettingsString
		}

		// 如果 HTTP2SettingsString 不为空，则解析并覆盖 HTTP2Settings 和 PHeaderOrderKeys
		if options.HTTP2SettingsString != "" {
			h2Settings, pHeaderOrderKeys, err := fastls.ParseH2SettingsStringWithPHeaderOrder(options.HTTP2SettingsString)
			if err != nil {
				log.Printf("解析 HTTP2SettingsString 失败: %v", err)
			} else {
				// 覆盖 HTTP2Settings
				options.HTTP2Settings = fastls.ToHTTP2Settings(h2Settings)
				// 如果解析出 PHeaderOrderKeys，则覆盖
				if len(pHeaderOrderKeys) > 0 {
					options.PHeaderOrderKeys = pHeaderOrderKeys
				}
			}
		}
	} else {
		// 处理请求体中的指纹配置（优先级：fingerprint > browser > 默认Firefox）
		if req.Fingerprint != nil {
			fpType, hasType := req.Fingerprint["type"]
			fpValue, hasValue := req.Fingerprint["value"]
			if hasType && hasValue {
				switch fpType {
				case "ja3":
					options.Fingerprint = fastls.Ja3Fingerprint{
						FingerprintValue: fpValue,
					}
				case "ja4", "ja4r":
					options.Fingerprint = fastls.Ja4Fingerprint{
						FingerprintValue: fpValue,
					}
				}
			}
		}

		// 如果没有指定指纹，根据浏览器类型设置指纹
		if (options.Fingerprint == nil || options.Fingerprint.IsEmpty()) && req.Browser != "" {
			switch req.Browser {
			case "chrome":
				imitate.Chrome(&options)
			case "chrome120":
				imitate.Chrome120(&options)
			case "chrome142":
				imitate.Chrome142(&options)
			case "chromium":
				imitate.Chromium(&options)
			case "edge":
				imitate.Edge(&options)
			case "firefox":
				imitate.Firefox(&options)
			case "firefox144":
				imitate.Firefox144(&options)
			case "firefox147":
				imitate.Firefox147(&options)
			case "firefox151":
				imitate.Firefox(&options)
			case "safari":
				imitate.Safari(&options)
			case "opera":
				imitate.Opera(&options)
			default:
				imitate.Firefox(&options)
			}

			// 如果设置了 Browser，使用 imitate 设置的 UserAgent（覆盖用户的 UserAgent）
			if options.UserAgent != "" {
				options.Headers["User-Agent"] = options.UserAgent
			}
		} else if req.UserAgent != "" {
			// 如果没有设置 Browser，使用用户提供的 UserAgent
			options.UserAgent = req.UserAgent
			options.Headers["User-Agent"] = req.UserAgent
		}

		// 如果既没有指定指纹也没有指定浏览器，使用默认 Firefox148 指纹
		if options.Fingerprint == nil || options.Fingerprint.IsEmpty() {
			imitate.Firefox147(&options)
			// 使用 imitate 设置的 UserAgent
			if options.UserAgent != "" {
				options.Headers["User-Agent"] = options.UserAgent
			}
		}
	}
	// 执行请求
	resp, err := client.Do(req.URL, options, req.Method)

	// 处理错误（在关闭Body之前检查）
	if err != nil {
		status := resp.Status
		if resp.Body != nil {
			resp.Body.Close()
		}
		c.JSON(http.StatusOK, FetchResponse{
			OK:     false,
			Status: status,
			Error:  err.Error(),
		})
		return
	}

	// 确保响应体被关闭
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	// 读取响应体
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusOK, FetchResponse{
			OK:     false,
			Status: resp.Status,
			Error:  "读取响应体失败: " + err.Error(),
		})
		return
	}

	// 解码响应体（处理gzip、br等压缩）
	contentEncoding := resp.Headers["Content-Encoding"]
	var decodedBody string
	if contentEncoding != "" {
		decodedBody = fastls.DecompressBody(bodyBytes, []string{contentEncoding}, nil)
	} else {
		// 如果没有Content-Encoding，检查Content-Type来决定是否需要base64编码
		contentType := resp.Headers["Content-Type"]
		if contentType != "" {
			decodedBody = fastls.DecompressBody(bodyBytes, nil, []string{contentType})
		} else {
			decodedBody = string(bodyBytes)
		}
	}

	// 返回成功响应
	c.JSON(http.StatusOK, FetchResponse{
		OK:      true,
		Status:  resp.Status,
		Headers: resp.Headers,
		Body:    decodedBody,
	})
}

// corsMiddleware CORS中间件
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
