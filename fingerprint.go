package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	fastls "github.com/FastTLS/fastls"
	fastlsimitate "github.com/FastTLS/fastls/imitate"
	generators "github.com/FastTLS/imitate"
)

var fingerprintProvider = generators.New()

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
func applyImitateConfig(options *fastls.Options, requestConfig *RequestFingerprintConfig) error {
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
			return nil
		}

		if requestConfig.JA4R != "" {
			options.Fingerprint = fastls.Ja4Fingerprint{
				FingerprintValue: requestConfig.JA4R,
			}
			return nil
		}

		if requestConfig.Browser != "" {
			return applyBrowserImitate(options, requestConfig.Browser)
		}
	}
	return nil
}

// applyBrowserImitate 按浏览器类型应用 imitate 指纹
func applyBrowserImitate(options *fastls.Options, browserType string) error {
	selector := browserSelector(browserType)
	_, err := fastlsimitate.Use(options, selector, fastlsimitate.WithProvider(fingerprintProvider))
	return err
}

func browserSelector(browserType string) string {
	browserType = strings.ToLower(strings.TrimSpace(browserType))
	selector := "firefox/158/windows"

	// firefox151–firefox158
	if strings.HasPrefix(browserType, "firefox") && browserType != "firefox" {
		if verStr := strings.TrimPrefix(browserType, "firefox"); verStr != "" {
			if major, err := strconv.Atoi(verStr); err == nil && (major >= 151 && major <= 158 || major == 144 || major == 147 || major == 148) {
				selector = fmt.Sprintf("firefox/%d/windows", major)
			}
		}
	}

	// chrome150–chrome160
	if strings.HasPrefix(browserType, "chrome") && browserType != "chrome" && browserType != "chromium" {
		if verStr := strings.TrimPrefix(browserType, "chrome"); verStr != "" {
			if major, err := strconv.Atoi(verStr); err == nil {
				if major == 120 || major == 142 || major >= 150 && major <= 160 {
					selector = fmt.Sprintf("chrome/%d/windows", major)
				}
			}
		}
	}

	// edge150–edge160
	if strings.HasPrefix(browserType, "edge") && browserType != "edge" {
		if verStr := strings.TrimPrefix(browserType, "edge"); verStr != "" {
			if major, err := strconv.Atoi(verStr); err == nil &&
				major >= 150 && major <= 160 {
				selector = fmt.Sprintf("edge/%d/windows", major)
			}
		}
	}

	switch browserType {
	case "chrome":
		selector = "chrome/160/windows"
	case "chrome120":
		selector = "chrome/120/windows"
	case "chrome142":
		selector = "chrome/142/windows"
	case "chromium":
		selector = "chromium/160/windows"
	case "edge":
		selector = "edge/160/windows"
	case "firefox":
		selector = "firefox/158/windows"
	case "firefox144":
		selector = "firefox/144/windows"
	case "firefox147":
		selector = "firefox/147/windows"
	case "firefox148":
		selector = "firefox/148/windows"
	case "safari":
		selector = "safari/18.7.3/ipados"
	case "opera":
		selector = "opera/145/windows"
	}
	return selector
}
