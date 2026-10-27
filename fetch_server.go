package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/FastTLS/fastls-fetch/internal/audit"
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
	"github.com/FastTLS/fastls-fetch/internal/ingress"
	"github.com/FastTLS/fastls-fetch/internal/responsecontract"
	"github.com/FastTLS/fastls-fetch/internal/servicepolicy"
	"github.com/gin-gonic/gin"
)

const (
	shutdownTimeout    = 5 * time.Second
	maxRequestBodySize = 4 << 20
)

// FetchRequest 请求结构体
type FetchRequest struct {
	IncludeBodyBase64   bool              `json:"include_body_base64"`
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
	OK           bool              `json:"ok"`
	Status       int               `json:"status"`
	Protocol     string            `json:"protocol,omitempty"`
	FinalURL     string            `json:"final_url,omitempty"`
	Headers      map[string]string `json:"headers"`
	Trailers     http.Header       `json:"trailers,omitempty"`
	Body         string            `json:"body"`
	Error        string            `json:"error,omitempty"`
	HeaderValues http.Header       `json:"header_values,omitempty"`
	BodyBase64   string            `json:"body_base64,omitempty"`
	ErrorCode    string            `json:"error_code,omitempty"`
	TimeoutPhase string            `json:"timeout_phase,omitempty"`
}

func main() {
	var (
		addr           string
		debug          bool
		showVersion    bool
		showHelp       bool
		maximum        int64
		requestMaximum int64
		inflight       int
		policyFile     string
		auditConfig    audit.Config
		ingressConfig  ingress.HTTPConfig
	)

	// 定义完整参数和缩写参数
	flag.StringVar(&addr, "addr", ":8800", "监听地址 (例如: :8800 或 0.0.0.0:8800)")
	flag.StringVar(&addr, "a", ":8800", "监听地址 (addr 的缩写)")
	flag.Int64Var(&maximum, "max-response-bytes", 0, "Maximum wire and decoded response bytes; 0 is unlimited")
	flag.Int64Var(&requestMaximum, "max-request-bytes", 4<<20, "Maximum complete JSON request bytes (1..64 MiB); 0 selects 4 MiB")
	flag.IntVar(&inflight, "max-inflight", 0, "Maximum active fetch requests; 0 is unlimited")
	flag.StringVar(&policyFile, "address-policy", "", "Trusted address policy JSON file; unsupported cores refuse startup")
	ingressConfig.BindFlags(flag.CommandLine)
	auditConfig.BindFlags(flag.CommandLine)

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
	if err := audit.Run(auditConfig, func(sink *audit.Sink) error {
		runtime.GOMAXPROCS(runtime.NumCPU())

		// 初始化Fastls客户端（全局单例，支持高并发）
		client := fastls.NewClient()

		// 设置gin模式
		if debug {
			gin.SetMode(gin.DebugMode)
		} else {
			gin.SetMode(gin.ReleaseMode)
		}
		r, err := newFetchRouterWithConfig(client, fetchServerConfig{Limits: responsecontract.Limits{MaxRequestBytes: requestMaximum, MaxResponseBytes: maximum, MaxInflight: inflight}, AddressPolicyFile: policyFile})
		if err != nil {
			return err
		}

		server, err := ingress.NewHTTPServer(r, ingressConfig)
		if err != nil {
			return err
		}
		server.Handler = sink.HTTP("fetch", server.Handler)
		// 启动服务器
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("监听端口失败: %w", err)
		}
		limited, err := ingress.NewListener(listener, ingressConfig.MaxConnections)
		if err != nil {
			listener.Close()
			return err
		}
		if sink != nil {
			limited.OnReject = func() { sink.Reject("fetch") }
		}
		shutdownContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stopSignals()
		log.Printf("Fetch服务启动在端口 %s", listener.Addr())
		if err := serveConfiguredHTTP(shutdownContext, limited, server, shutdownTimeout); err != nil {
			return fmt.Errorf("服务器启动失败: %w", err)
		}
		if shutdownContext.Err() != nil {
			log.Printf("Fetch服务已安全停止")
		}
		return nil
	}); err != nil {
		log.Fatal(err)
	}
}

func newFetchRouter(client fastls.Fastls) *gin.Engine {
	r, _ := newFetchRouterWithLimits(client, responsecontract.Limits{})
	return r
}

func fetchMaximum(c *gin.Context) int64 {
	value, _ := c.Get("response.maximum")
	maximum, _ := value.(int64)
	return maximum
}

func newFetchRouterWithLimits(client fastls.Fastls, limits responsecontract.Limits) (*gin.Engine, error) {
	return newFetchRouterWithConfig(client, fetchServerConfig{Limits: limits})
}

type fetchServerConfig struct {
	Limits            responsecontract.Limits
	AddressPolicyFile string
}

func newFetchRouterWithConfig(client fastls.Fastls, config fetchServerConfig) (*gin.Engine, error) {
	guard, err := responsecontract.NewGuard(config.Limits)
	if err != nil {
		return nil, err
	}
	client, err = servicepolicy.Load(client, config.AddressPolicyFile)
	if err != nil {
		return nil, err
	}
	r := gin.Default()
	r.Use(corsMiddleware())
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.POST("/fetch", func(c *gin.Context) {
		release, err := guard.Acquire(c.Request.Context())
		if err != nil {
			code, phase := responsecontract.Code(err, "overloaded")
			c.JSON(http.StatusServiceUnavailable, FetchResponse{Error: err.Error(), ErrorCode: code, TimeoutPhase: phase})
			return
		}
		defer release()
		requestBodyLimit(guard.RequestMaximum())(c)
		if c.IsAborted() {
			return
		}
		c.Set("response.maximum", guard.Maximum())
		handleFetch(c, client)
	})
	return r, nil
}

func serveHTTP(ctx context.Context, listener net.Listener, handler http.Handler) error {
	return serveHTTPWithTimeout(ctx, listener, handler, shutdownTimeout)
}

func serveHTTPWithTimeout(ctx context.Context, listener net.Listener, handler http.Handler, timeout time.Duration) error {
	server, err := ingress.NewHTTPServer(handler, ingress.HTTPConfig{})
	if err != nil {
		return err
	}
	return serveConfiguredHTTP(ctx, listener, server, timeout)
}

func serveConfiguredHTTP(ctx context.Context, listener net.Listener, server *http.Server, timeout time.Duration) error {
	if ctx == nil {
		return errors.New("nil server context")
	}
	if listener == nil || server == nil {
		return errors.New("nil HTTP handler or listener")
	}
	if timeout <= 0 {
		return errors.New("shutdown timeout must be positive")
	}
	if closer, ok := listener.(interface{ CloseConnections() error }); ok {
		defer closer.CloseConnections()
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
			writeFetchResponse(c, http.StatusRequestEntityTooLarge, FetchResponse{
				OK:    false,
				Error: fmt.Sprintf("请求体超过 %d 字节限制", maxBytesError.Limit),
			})
			return
		}
		writeFetchResponse(c, http.StatusBadRequest, FetchResponse{
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
		writeFetchResponse(c, http.StatusBadRequest, FetchResponse{OK: false, Error: "timeout 不能为负数"})
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
		writeFetchResponse(c, http.StatusBadRequest, FetchResponse{OK: false, Error: "代理配置错误: " + err.Error()})
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
			writeFetchResponse(c, http.StatusBadRequest, FetchResponse{OK: false, Error: "浏览器指纹配置错误: " + err.Error()})
			return
		}
		if (options.Fingerprint == nil || options.Fingerprint.IsEmpty()) && options.Profile == nil {
			if err := applyBrowserImitate(&options, "firefox"); err != nil {
				writeFetchResponse(c, http.StatusInternalServerError, FetchResponse{OK: false, Error: "默认浏览器指纹不可用: " + err.Error()})
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
				writeFetchResponse(c, http.StatusBadRequest, FetchResponse{OK: false, Error: "指纹配置必须同时包含 type 和 value"})
				return
			}
			switch fpType {
			case "ja3":
				bodyConfig.JA3 = fpValue
			case "ja4", "ja4r":
				bodyConfig.JA4R = fpValue
			default:
				writeFetchResponse(c, http.StatusBadRequest, FetchResponse{OK: false, Error: "不支持的指纹类型: " + fpType})
				return
			}
		}
		bodyConfig.Override = bodyConfig.Browser != "" || bodyConfig.JA3 != "" || bodyConfig.JA4R != "" || bodyConfig.HTTP2SettingsString != ""

		if bodyConfig.Override {
			if err := applyImitateConfig(&options, bodyConfig); err != nil {
				writeFetchResponse(c, http.StatusBadRequest, FetchResponse{OK: false, Error: "浏览器指纹配置错误: " + err.Error()})
				return
			}
			options.HTTP2SettingsString = bodyConfig.HTTP2SettingsString
		}

		// 如果既没有指定指纹也没有指定浏览器，使用默认最新 Firefox
		if options.Fingerprint == nil || options.Fingerprint.IsEmpty() {
			if err := applyBrowserImitate(&options, "firefox"); err != nil {
				writeFetchResponse(c, http.StatusInternalServerError, FetchResponse{OK: false, Error: "默认浏览器指纹不可用: " + err.Error()})
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
			writeFetchResponse(c, http.StatusBadRequest, FetchResponse{OK: false, Error: "HTTP/2 设置错误: " + err.Error()})
			return
		}
	}
	// 执行请求
	resp, err := client.DoContext(c.Request.Context(), req.URL, options, req.Method)
	defer resp.Client.CloseIdleConnections()

	// 处理错误（在关闭Body之前检查）
	if err != nil {
		status := resp.Status
		code, phase := responsecontract.Code(err, "request_error")
		if resp.Body != nil {
			resp.Body.Close()
		}
		writeFetchResponse(c, http.StatusOK, FetchResponse{
			OK:        false,
			Status:    status,
			Protocol:  resp.Protocol,
			FinalURL:  resp.FinalURL,
			Error:     responsecontract.ErrorMessage(err),
			ErrorCode: code, TimeoutPhase: phase,
		})
		return
	}

	if resp.Body != nil {
		defer resp.Body.Close()
	}

	// 读取响应体
	wire, decodedBody, err := responsecontract.Read(resp.Body, http.Header(resp.HeaderValues), fetchMaximum(c))
	if err != nil {
		code, phase := responsecontract.Code(err, "read_error")
		writeFetchResponse(c, http.StatusOK, FetchResponse{
			OK:       false,
			Status:   resp.Status,
			Error:    "读取响应体失败: " + err.Error(),
			Protocol: resp.Protocol, FinalURL: resp.FinalURL, Headers: resp.Headers, HeaderValues: http.Header(resp.HeaderValues).Clone(), ErrorCode: code, TimeoutPhase: phase,
		})
		return
	}

	bodyBase64 := ""
	if req.IncludeBodyBase64 {
		bodyBase64 = responsecontract.Base64(wire)
	}
	// 返回成功响应
	writeFetchResponse(c, http.StatusOK, FetchResponse{
		OK:           true,
		Status:       resp.Status,
		Protocol:     resp.Protocol,
		FinalURL:     resp.FinalURL,
		Headers:      resp.Headers,
		Trailers:     resp.Trailer.Clone(),
		Body:         decodedBody,
		HeaderValues: http.Header(resp.HeaderValues).Clone(), BodyBase64: bodyBase64,
	})
}

func requestBodyLimit(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			body := http.MaxBytesReader(c.Writer, c.Request.Body, limit)
			data, err := io.ReadAll(body)
			body.Close()
			if err != nil {
				status, code := http.StatusBadRequest, "request_error"
				message := err.Error()
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					status, code = http.StatusRequestEntityTooLarge, "request_too_large"
					message = fmt.Sprintf("request body exceeds %d byte limit", tooLarge.Limit)
				}
				c.AbortWithStatusJSON(status, FetchResponse{Error: message, ErrorCode: code})
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(data))
		}
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

func writeFetchResponse(c *gin.Context, status int, result FetchResponse) {
	audit.Result(c.Request.Context(), result.Protocol, result.Status, result.ErrorCode, result.TimeoutPhase)
	c.JSON(status, result)
}
