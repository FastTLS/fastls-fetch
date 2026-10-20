package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	fastls "github.com/FastTLS/fastls"
	"github.com/FastTLS/fastls/imitate"
)

type RequestFingerprintConfig struct {
	Browser             string
	JA3                 string
	JA4R                string
	HTTP2SettingsString string
	Override            bool
}

func parseFingerprintFromHeaders(headers http.Header) *RequestFingerprintConfig {
	config := &RequestFingerprintConfig{}

	if browser := headers.Get("X-Mitm-Browser"); browser != "" {
		config.Browser = browser
		config.Override = true
	}

	if ja3 := headers.Get("X-Mitm-Ja3"); ja3 != "" {
		config.JA3 = ja3
		config.Override = true
	}

	if ja4r := headers.Get("X-Mitm-Ja4r"); ja4r != "" {
		config.JA4R = ja4r
		config.Override = true
	}

	if http2SettingsString := headers.Get("X-Mitm-H2Settings"); http2SettingsString != "" {
		config.HTTP2SettingsString = http2SettingsString
		config.Override = true
	}

	return config
}

// parseProxyFromHeaders 从请求头解析代理信息
// 支持请求头: X-Mitm-Proxy
// 格式: http://proxy.example.com:8080 或 http://user:pass@proxy.example.com:8080
func parseProxyFromHeaders(headers http.Header) (*url.URL, error) {
	proxyStr := headers.Get("X-Mitm-Proxy")
	if proxyStr == "" {
		return nil, nil
	}

	proxyURL, err := url.Parse(proxyStr)
	if err != nil {
		return nil, fmt.Errorf("解析代理URL失败: %v", err)
	}

	// 验证代理协议
	if proxyURL.Scheme != "http" && proxyURL.Scheme != "https" && proxyURL.Scheme != "socks5" {
		return nil, fmt.Errorf("不支持的代理协议: %s (仅支持 http, https, socks5)", proxyURL.Scheme)
	}

	return proxyURL, nil
}

// applyImitateConfig 应用指纹配置到 options
func applyImitateConfig(options *fastls.Options, requestConfig *RequestFingerprintConfig) {
	if requestConfig != nil && requestConfig.Override {
		fingerprintCount := 0
		if requestConfig.JA3 != "" {
			fingerprintCount++
		}
		if requestConfig.JA4R != "" {
			fingerprintCount++
		}
		if requestConfig.Browser != "" {
			fingerprintCount++
		}
		_ = fingerprintCount

		// 优先级: JA3 > JA4R > Browser
		if requestConfig.JA3 != "" {
			options.Fingerprint = fastls.Ja3Fingerprint{
				FingerprintValue: requestConfig.JA3,
			}
			return
		}

		if requestConfig.JA4R != "" {
			options.Fingerprint = fastls.Ja4Fingerprint{
				FingerprintValue: requestConfig.JA4R,
			}
			return
		}

		if requestConfig.Browser != "" {
			applyBrowserImitate(options, requestConfig.Browser)
			return
		}
	}
}

func finishBrowserUA(options *fastls.Options) {
	if options.UserAgent != "" {
		options.Headers["User-Agent"] = options.UserAgent
	}
}

// applyBrowserImitate 按浏览器类型应用 imitate 指纹
func applyBrowserImitate(options *fastls.Options, browserType string) {
	browserType = strings.ToLower(strings.TrimSpace(browserType))

	// firefox151–firefox158
	if strings.HasPrefix(browserType, "firefox") && browserType != "firefox" {
		if verStr := strings.TrimPrefix(browserType, "firefox"); verStr != "" {
			if major, err := strconv.Atoi(verStr); err == nil && major >= 151 && major <= 158 {
				imitate.FirefoxVersion(options, major)
				finishBrowserUA(options)
				return
			}
		}
	}

	// chrome150–chrome160
	if strings.HasPrefix(browserType, "chrome") && browserType != "chrome" && browserType != "chromium" {
		if verStr := strings.TrimPrefix(browserType, "chrome"); verStr != "" {
			if major, err := strconv.Atoi(verStr); err == nil {
				switch major {
				case 120:
					imitate.Chrome120(options)
					finishBrowserUA(options)
					return
				case 142:
					imitate.Chrome142(options)
					finishBrowserUA(options)
					return
				}
				if major >= imitate.ChromiumVersionMin && major <= imitate.ChromiumVersionMax {
					imitate.ChromeVersion(options, major)
					finishBrowserUA(options)
					return
				}
			}
		}
	}

	// edge150–edge160
	if strings.HasPrefix(browserType, "edge") && browserType != "edge" {
		if verStr := strings.TrimPrefix(browserType, "edge"); verStr != "" {
			if major, err := strconv.Atoi(verStr); err == nil &&
				major >= imitate.ChromiumVersionMin && major <= imitate.ChromiumVersionMax {
				imitate.EdgeVersion(options, major)
				finishBrowserUA(options)
				return
			}
		}
	}

	switch browserType {
	case "chrome":
		imitate.Chrome(options)
	case "chrome120":
		imitate.Chrome120(options)
	case "chrome142":
		imitate.Chrome142(options)
	case "chromium":
		imitate.Chromium(options)
	case "edge":
		imitate.Edge(options)
	case "firefox":
		imitate.Firefox158(options)
	case "firefox144":
		imitate.Firefox144(options)
	case "firefox147":
		imitate.Firefox147(options)
	case "firefox148":
		imitate.Firefox148(options)
	case "safari":
		imitate.Safari(options)
	case "opera":
		imitate.Opera(options)
	default:
		imitate.Firefox158(options)
	}
	finishBrowserUA(options)
}
