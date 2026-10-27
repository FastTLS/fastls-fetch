package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	fastls "github.com/FastTLS/fastls"
	"github.com/gin-gonic/gin"
)

const (
	shutdownTimeout    = 5 * time.Second
	maxRequestBodySize = 4 << 20
)

// FetchRequest 请求结构体
type FetchRequest struct {
	URL                 string            `json:"url" binding:"required"`
	Method              string            `json:"method"`
	Headers             map[string]string `json:"headers"`
	Trailers            http.Header       `json:"trailers,omitempty"`
	Body                string            `json:"body"`
	Proxy               string            `json:"proxy"`
	Timeout             int               `json:"timeout"`
	DisableRedirect     bool              `json:"disableRedirect"`
	UserAgent           string            `json:"userAgent"`
	Fingerprint         map[string]string `json:"fingerprint"` // {"type": "ja3", "value": "..."} 或 {"type": "ja4r", "value": "..."}
	Browser             string            `json:"browser"`     // chrome/chrome160, chrome150..160, chrome120/142, edge/edge160, edge150..160, firefox/firefox158, firefox151..158, safari, opera
	Cookies             []fastls.Cookie   `json:"cookies"`
	HTTP2SettingsString string            `json:"http2_settings_string"`
	ParentProxy         string            `json:"parent_proxy"`
}

// FetchResponse 响应结构体
type FetchResponse struct {
	OK       bool              `json:"ok"`
	Status   int               `json:"status"`
	Protocol string            `json:"protocol,omitempty"`
	FinalURL string            `json:"final_url,omitempty"`
	Headers  map[string]string `json:"headers"`
	Trailers http.Header       `json:"trailers,omitempty"`
	Body     string            `json:"body"`
	Error    string            `json:"error,omitempty"`
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
	r := newFetchRouter(client)

	// 启动服务器
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("监听端口失败: %v", err)
	}
	shutdownContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	log.Printf("Fetch服务启动在端口 %s", listener.Addr())
	if err := serveHTTP(shutdownContext, listener, r); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
	if shutdownContext.Err() != nil {
		log.Printf("Fetch服务已安全停止")
	}
}

func newFetchRouter(client fastls.Fastls) *gin.Engine {
	r := gin.Default()
	r.Use(requestBodyLimit(maxRequestBodySize))
	r.Use(corsMiddleware())
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.POST("/fetch", func(c *gin.Context) {
		handleFetch(c, client)
	})
	return r
}

func serveHTTP(ctx context.Context, listener net.Listener, handler http.Handler) error {
	return serveHTTPWithTimeout(ctx, listener, handler, shutdownTimeout)
}

func serveHTTPWithTimeout(ctx context.Context, listener net.Listener, handler http.Handler, timeout time.Duration) error {
	if ctx == nil {
		return errors.New("nil server context")
	}
	if listener == nil || handler == nil {
		return errors.New("nil HTTP handler or listener")
	}
	if timeout <= 0 {
		return errors.New("shutdown timeout must be positive")
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	shutdownResult := make(chan error, 1)
	stopCancellation := context.AfterFunc(ctx, func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownContext)
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, server.Close())
		}
		shutdownResult <- shutdownErr
	})
	err := server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	if stopCancellation() {
		return err
	}
	return errors.Join(err, <-shutdownResult)
}

// handleFetch 处理fetch请求
func handleFetch(c *gin.Context, client fastls.Fastls) {
	var req FetchRequest

	// 绑定JSON请求体
	if err := c.ShouldBindJSON(&req); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, FetchResponse{
				OK:    false,
				Error: fmt.Sprintf("请求体超过 %d 字节限制", maxBytesError.Limit),
			})
			return
		}
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
	if req.Timeout < 0 {
		c.JSON(http.StatusBadRequest, FetchResponse{OK: false, Error: "timeout 不能为负数"})
		return
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
		Trailers:        req.Trailers,
		Proxy:           req.Proxy,
		Timeout:         req.Timeout,
		DisableRedirect: req.DisableRedirect,
		Cookies:         req.Cookies,
	}

	// 解析请求头中的指纹配置（优先级最高）
	requestConfig := parseFingerprintFromHeaders(c.Request.Header)
	headerProxy, err := parseProxyFromHeaders(c.Request.Header)
	if err != nil {
		c.JSON(http.StatusBadRequest, FetchResponse{OK: false, Error: "代理配置错误: " + err.Error()})
		return
	}

	// 先复制用户的请求头到options（排除 X-Mitm-* 头）
	for key, value := range req.Headers {
		if !strings.HasPrefix(strings.ToLower(key), "x-mitm-") {
			options.Headers[key] = value
		}
	}

	// 优先使用请求头中的代理信息
	if headerProxy != nil {
		options.Proxy = headerProxy.String()
	} else if req.ParentProxy != "" {
		options.Proxy = req.ParentProxy
	} else if req.Proxy != "" {
		options.Proxy = req.Proxy
	}

	// 优先使用请求头中的指纹配置
	if requestConfig != nil && requestConfig.Override {
		if err := applyImitateConfig(&options, requestConfig); err != nil {
			c.JSON(http.StatusBadRequest, FetchResponse{OK: false, Error: "浏览器指纹配置错误: " + err.Error()})
			return
		}
		if (options.Fingerprint == nil || options.Fingerprint.IsEmpty()) && options.Profile == nil {
			if err := applyBrowserImitate(&options, "firefox"); err != nil {
				c.JSON(http.StatusInternalServerError, FetchResponse{OK: false, Error: "默认浏览器指纹不可用: " + err.Error()})
				return
			}
		}

		// 优先使用请求头中的 HTTP2SettingsString，如果存在则覆盖
		if requestConfig.HTTP2SettingsString != "" {
			options.HTTP2SettingsString = requestConfig.HTTP2SettingsString
		}
	} else {
		// 处理请求体中的指纹配置（优先级：fingerprint > browser > 默认Firefox）
		bodyConfig := &RequestFingerprintConfig{
			Browser:             req.Browser,
			HTTP2SettingsString: req.HTTP2SettingsString,
		}
		if req.Fingerprint != nil {
			fpType, hasType := req.Fingerprint["type"]
			fpValue, hasValue := req.Fingerprint["value"]
			if !hasType || !hasValue || fpType == "" || fpValue == "" {
				c.JSON(http.StatusBadRequest, FetchResponse{OK: false, Error: "指纹配置必须同时包含 type 和 value"})
				return
			}
			switch fpType {
			case "ja3":
				bodyConfig.JA3 = fpValue
			case "ja4", "ja4r":
				bodyConfig.JA4R = fpValue
			default:
				c.JSON(http.StatusBadRequest, FetchResponse{OK: false, Error: "不支持的指纹类型: " + fpType})
				return
			}
		}
		bodyConfig.Override = bodyConfig.Browser != "" || bodyConfig.JA3 != "" || bodyConfig.JA4R != "" || bodyConfig.HTTP2SettingsString != ""

		if bodyConfig.Override {
			if err := applyImitateConfig(&options, bodyConfig); err != nil {
				c.JSON(http.StatusBadRequest, FetchResponse{OK: false, Error: "浏览器指纹配置错误: " + err.Error()})
				return
			}
			options.HTTP2SettingsString = bodyConfig.HTTP2SettingsString
		}

		// 如果既没有指定指纹也没有指定浏览器，使用默认最新 Firefox
		if options.Fingerprint == nil || options.Fingerprint.IsEmpty() {
			if err := applyBrowserImitate(&options, "firefox"); err != nil {
				c.JSON(http.StatusInternalServerError, FetchResponse{OK: false, Error: "默认浏览器指纹不可用: " + err.Error()})
				return
			}
		}
		// 浏览器配置会带入自身的 HTTP/2 设置；请求体中的显式值最后覆盖它。
		if bodyConfig.HTTP2SettingsString != "" {
			options.HTTP2SettingsString = bodyConfig.HTTP2SettingsString
		}
	}

	// 显式 User-Agent 覆盖浏览器配置生成的默认值；headers 中的 User-Agent 仍具有最高优先级。
	if req.UserAgent != "" {
		options.UserAgent = req.UserAgent
	}
	if options.HTTP2SettingsString != "" {
		if _, _, err := fastls.ParseH2SettingsStringWithPHeaderOrder(options.HTTP2SettingsString); err != nil {
			c.JSON(http.StatusBadRequest, FetchResponse{OK: false, Error: "HTTP/2 设置错误: " + err.Error()})
			return
		}
	}
	// 执行请求
	resp, err := client.DoContext(c.Request.Context(), req.URL, options, req.Method)
	defer resp.Client.CloseIdleConnections()

	// 处理错误（在关闭Body之前检查）
	if err != nil {
		status := resp.Status
		if resp.Body != nil {
			resp.Body.Close()
		}
		c.JSON(http.StatusOK, FetchResponse{
			OK:       false,
			Status:   status,
			Protocol: resp.Protocol,
			FinalURL: resp.FinalURL,
			Error:    err.Error(),
		})
		return
	}

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
		OK:       true,
		Status:   resp.Status,
		Protocol: resp.Protocol,
		FinalURL: resp.FinalURL,
		Headers:  resp.Headers,
		Trailers: resp.Trailer.Clone(),
		Body:     decodedBody,
	})
}

func requestBodyLimit(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

// corsMiddleware CORS中间件
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		} else {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			c.Writer.Header().Add("Vary", "Origin")
		}
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, Accept, Origin, Cache-Control, X-Requested-With, X-Mitm-Browser, X-Mitm-Ja3, X-Mitm-Ja4r, X-Mitm-H2Settings, X-Mitm-Proxy")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.Writer.Header().Add("Vary", "Access-Control-Request-Method")
			c.Writer.Header().Add("Vary", "Access-Control-Request-Headers")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
