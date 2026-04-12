package save

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/beck-8/subs-check/check"
	"github.com/beck-8/subs-check/config"
	"github.com/beck-8/subs-check/save/method"
	"github.com/beck-8/subs-check/utils"
	"gopkg.in/yaml.v3"
)

// ProxyCategory 定义代理分类
type ProxyCategory struct {
	Name    string
	Proxies []map[string]any
	Filter  func(result check.Result) bool
}

// ConfigSaver 处理配置保存的结构体
type ConfigSaver struct {
	results    []check.Result
	categories []ProxyCategory
	saveMethod func([]byte, string) error
}

// NewConfigSaver 创建新的配置保存器
func NewConfigSaver(results []check.Result) *ConfigSaver {
	return &ConfigSaver{
		results:    results,
		saveMethod: chooseSaveMethod(),
		categories: []ProxyCategory{
			{
				Name:    "all.yaml",
				Proxies: make([]map[string]any, 0),
				Filter:  func(result check.Result) bool { return true },
			},
			{
				Name:    "mihomo.yaml",
				Proxies: make([]map[string]any, 0),
				Filter:  func(result check.Result) bool { return true },
			},
			{
				Name:    "base64.txt",
				Proxies: make([]map[string]any, 0),
				Filter:  func(result check.Result) bool { return true },
			},
		},
	}
}

// SaveConfig 保存配置的入口函数
func SaveConfig(results []check.Result) {
	tmp := config.GlobalConfig.SaveMethod
	config.GlobalConfig.SaveMethod = "local"
	// 奇技淫巧，保存到本地一份，因为我没想道其他更好的方法同时保存
	{
		saver := NewConfigSaver(results)
		if err := saver.Save(); err != nil {
			slog.Error(fmt.Sprintf("保存配置失败: %v", err))
		}
	}

	if tmp == "local" {
		return
	}
	config.GlobalConfig.SaveMethod = tmp
	// 如果其他配置验证失败，还会保存到本地一次
	{
		saver := NewConfigSaver(results)
		if err := saver.Save(); err != nil {
			slog.Error(fmt.Sprintf("保存配置失败: %v", err))
		}
	}
}

// Save 执行保存操作
func (cs *ConfigSaver) Save() error {
	// 分类处理代理
	cs.categorizeProxies()

	// 保存各个类别的代理
	for _, category := range cs.categories {
		if err := cs.saveCategory(category); err != nil {
			slog.Error(fmt.Sprintf("保存到%s失败: %v", config.GlobalConfig.SaveMethod, err))
			continue
		}
	}

	return nil
}

// categorizeProxies 将代理按类别分类
func (cs *ConfigSaver) categorizeProxies() {
	for _, result := range cs.results {
		for i := range cs.categories {
			if cs.categories[i].Filter(result) {
				cs.categories[i].Proxies = append(cs.categories[i].Proxies, result.Proxy)
			}
		}
	}
}

// saveCategory 保存单个类别的代理
func (cs *ConfigSaver) saveCategory(category ProxyCategory) error {
	if len(category.Proxies) == 0 {
		slog.Warn(fmt.Sprintf("yaml节点为空，跳过保存: %s, saveMethod: %s", category.Name, config.GlobalConfig.SaveMethod))
		return nil
	}

	if category.Name == "all.yaml" {
		yamlData, err := yaml.Marshal(map[string]any{
			"proxies": category.Proxies,
		})
		if err != nil {
			return fmt.Errorf("序列化yaml %s 失败: %w", category.Name, err)
		}
		if err := cs.saveMethod(yamlData, category.Name); err != nil {
			return fmt.Errorf("保存 %s 失败: %w", category.Name, err)
		}
		// 只在 all.yaml 和 local时，更新substore
		if config.GlobalConfig.SaveMethod == "local" && config.GlobalConfig.SubStorePort != "" {
			utils.UpdateSubStore(yamlData)
		}
		return nil
	}

	// mihomo.yaml 通过 sub-store 获取内容的场景
	if category.Name == "mihomo.yaml" && config.GlobalConfig.SubStorePort != "" {
		resp, err := http.Get(fmt.Sprintf("%s/api/file/%s", utils.BaseURL, utils.MihomoName))
		if err != nil {
			return fmt.Errorf("获取mihomo file请求失败: %w", err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("读取mihomo file失败: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("获取mihomo file失败, 状态码: %d, 错误信息: %s", resp.StatusCode, body)
		}

		// 对比逻辑（同上）
		if config.GlobalConfig.SaveMethod == "local" {
			prev, err := readLocalFileIfExists(category.Name)
			if err != nil {
				newKeys, _ := extractProxyKeysFromYAML(body)
				slog.Info("新增代理", "count", len(newKeys), "file", category.Name)
			} else {
				newCount, err := countNewProxyKeysFromYAML(body, prev)
				if err != nil {
					slog.Warn(fmt.Sprintf("比较 %s 时发生错误: %v", category.Name, err))
				} else {
					slog.Info("新增代理", "count", newCount, "file", category.Name)
				}
			}
		}

		if err := cs.saveMethod(body, category.Name); err != nil {
			return fmt.Errorf("保存 %s 失败: %w", category.Name, err)
		}
		return nil
	}

	// base64.txt 等其他情况保持原样
	if category.Name == "base64.txt" && config.GlobalConfig.SubStorePort != "" {
		// http://127.0.0.1:8299/download/sub?target=V2Ray
		resp, err := http.Get(fmt.Sprintf("%s/download/%s?target=V2Ray", utils.BaseURL, utils.SubName))
		if err != nil {
			return fmt.Errorf("获取base64.txt请求失败: %w", err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("读取base64.txt失败: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("获取base64.txt失败，状态码: %d, 错误信息: %s", resp.StatusCode, body)
		}
		if err := cs.saveMethod(body, category.Name); err != nil {
			return fmt.Errorf("保存 %s 失败: %w", category.Name, err)
		}
		return nil
	}

	return nil
}

// readLocalFileIfExists 尝试读取 output 目录下的已有文件，如果不存在则返error（由调用方决定如何处理）
func readLocalFileIfExists(filename string) ([]byte, error) {
	ls, err := method.NewLocalSaver()
	if err != nil {
		return nil, err
	}
	fp := ls.OutputPath + string(os.PathSeparator) + filename
	b, err := os.ReadFile(fp)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// buildProxyKey 从单个代理 map 中提取 server/ip + port + type 的唯一 key
func buildProxyKeyFromMap(proxy map[string]any) string {
	// 可能的 server 字段：server, address, ip
	server := ""
	if v, ok := proxy["server"].(string); ok && v != "" {
		server = v
	} else if v, ok := proxy["address"].(string); ok && v != "" {
		server = v
	} else if v, ok := proxy["ip"].(string); ok && v != "" {
		server = v
	}

	port := fmt.Sprintf("%v", proxy["port"])
	ptype := ""
	if v, ok := proxy["type"].(string); ok {
		ptype = v
	}

	key := strings.ToLower(strings.TrimSpace(server)) + ":" + strings.TrimSpace(port) + ":" + strings.ToLower(strings.TrimSpace(ptype))
	return key
}

// extractProxyKeysFromYAML 从 YAML 数据中抽取 proxies 列表并返回按 (server/ip:port:type) 生成的 key 集合
func extractProxyKeysFromYAML(data []byte) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	if len(data) == 0 {
		return out, nil
	}

	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("解析yaml失败: %w", err)
	}
	raw, ok := m["proxies"]
	if !ok || raw == nil {
		return out, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		// 有时候yaml解出来类型可能是 []interface{}
		return out, fmt.Errorf("proxies 字段格式不正确")
	}
	for _, it := range arr {
		// it 可能是 map[string]any 或 map[interface{}]interface{}
		pm := normalizeProxyInterfaceToMap(it)
		if pm == nil {
			continue
		}
		key := buildProxyKeyFromMap(pm)
		if key == ":" || key == "::" {
			// 略过无效键
			continue
		}
		out[key] = struct{}{}
	}
	return out, nil
}

// normalizeProxyInterfaceToMap 将不确定类型的 proxy 条目转换为 map[string]any（尽量兼容 map[interface{}]interface{}）
func normalizeProxyInterfaceToMap(it any) map[string]any {
	if it == nil {
		return nil
	}
	switch v := it.(type) {
	case map[string]any:
		return v
	case map[interface{}]interface{}:
		m := make(map[string]any)
		for kk, vv := range v {
			kstr := fmt.Sprintf("%v", kk)
			// 如果内部仍然是 map[interface{}]interface{}，不再递归深转（我们只需顶级字段）
			m[kstr] = vv
		}
		return m
	default:
		return nil
	}
}

// countNewProxyKeysFromYAML 对比 newYAML 与 oldYAML，按照 key 集合计算 new - old 的数量
func countNewProxyKeysFromYAML(newYAML, oldYAML []byte) (int, error) {
	newKeys, err := extractProxyKeysFromYAML(newYAML)
	if err != nil {
		return 0, err
	}
	oldKeys, err := extractProxyKeysFromYAML(oldYAML)
	if err != nil {
		// 如果解析旧文件失败，则把所有 newKeys 计为新增
		return len(newKeys), nil
	}
	newCount := 0
	for k := range newKeys {
		if _, ok := oldKeys[k]; !ok {
			newCount++
		}
	}
	return newCount, nil
}

// chooseSaveMethod 根据配置选择保存方法
func chooseSaveMethod() func([]byte, string) error {
	switch config.GlobalConfig.SaveMethod {
	case "r2":
		if err := method.ValiR2Config(); err != nil {
			return func(b []byte, s string) error { return fmt.Errorf("R2配置不完整: %v", err) }
		}
		return method.UploadToR2Storage
	case "gist":
		if err := method.ValiGistConfig(); err != nil {
			return func(b []byte, s string) error { return fmt.Errorf("Gist配置不完整: %v", err) }
		}
		return method.UploadToGist
	case "webdav":
		if err := method.ValiWebDAVConfig(); err != nil {
			return func(b []byte, s string) error { return fmt.Errorf("WebDAV配置不完整: %v", err) }
		}
		return method.UploadToWebDAV
	case "local":
		return method.SaveToLocal
	case "s3": // New case for MinIO
		if err := method.ValiS3Config(); err != nil {
			return func(b []byte, s string) error { return fmt.Errorf("S3配置不完整: %v", err) }
		}
		return method.UploadToS3
	default:
		return func(b []byte, s string) error {
			return fmt.Errorf("未知的保存方法或其他方法配置错误: %v", config.GlobalConfig.SaveMethod)
		}
	}
}
