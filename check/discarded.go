package check

import (
    "encoding/json"
    "fmt"
    "log/slog"
    "os"
    "path/filepath"
    "sync"
    "time"
)

// DiscardedProxy 记录被丢弃的节点信息
type DiscardedProxy struct {
    Proxy     map[string]any `json:"proxy"`
    Reason    string         `json:"reason"`
    Speed     int            `json:"speed"`
    Timestamp string         `json:"timestamp"`
}

// DiscardRecorder 管理丢弃的节点
type DiscardRecorder struct {
    mu        sync.Mutex
    discarded []DiscardedProxy
    filePath  string
}

var discardRecorder *DiscardRecorder

// InitDiscardRecorder 初始化丢弃记录器
func InitDiscardRecorder(outputDir string) error {
    if outputDir == "" {
        outputDir = "."
    }
    if err := os.MkdirAll(outputDir, 0755); err != nil {
        return fmt.Errorf("创建输出目录失败: %w", err)
    }
    filePath := filepath.Join(outputDir, "discarded_nodes.json")

    recorder := &DiscardRecorder{
        filePath:  filePath,
        discarded: make([]DiscardedProxy, 0),
    }

    if err := recorder.load(); err != nil {
        slog.Debug(fmt.Sprintf("加载丢弃记录失败，可能是首次运行: %v", err))
    }

    discardRecorder = recorder
    return nil
}

// RecordDiscard 记录一个被丢弃的节点
func RecordDiscard(proxy map[string]any, reason string, speed int) {
    if discardRecorder == nil {
        return
    }

    discardRecorder.mu.Lock()
    defer discardRecorder.mu.Unlock()

    discarded := DiscardedProxy{
        Proxy:     proxy,
        Reason:    reason,
        Speed:     speed,
        Timestamp: time.Now().Format("2006-01-02 15:04:05"),
    }
    discardRecorder.discarded = append(discardRecorder.discarded, discarded)
}

// GetDiscardedProxies 获取丢弃记录
func GetDiscardedProxies() []DiscardedProxy {
    if discardRecorder == nil {
        return []DiscardedProxy{}
    }
    discardRecorder.mu.Lock()
    defer discardRecorder.mu.Unlock()
    return discardRecorder.discarded
}

// Save 保存丢弃记录到文件
func (dr *DiscardRecorder) Save() error {
    dr.mu.Lock()
    defer dr.mu.Unlock()

    data, err := json.MarshalIndent(dr.discarded, "", "  ")
    if err != nil {
        return fmt.Errorf("序列化丢弃记录失败: %w", err)
    }

    if err := os.WriteFile(dr.filePath, data, 0644); err != nil {
        return fmt.Errorf("写入丢弃记录文件失败: %w", err)
    }
    return nil
}

// load 从文件加载丢弃记录
func (dr *DiscardRecorder) load() error {
    data, err := os.ReadFile(dr.filePath)
    if err != nil {
        return fmt.Errorf("读取丢弃记录文件失败: %w", err)
    }
    if err := json.Unmarshal(data, &dr.discarded); err != nil {
        return fmt.Errorf("反序列化丢弃记录失败: %w", err)
    }
    return nil
}

// RemoveDiscardedProxies 从节点列表中移除上一次的丢弃节点
func RemoveDiscardedProxies(proxies []map[string]any, discarded []DiscardedProxy) []map[string]any {
    if len(discarded) == 0 {
        return proxies
    }

    discardedMap := make(map[string]bool)
    for _, d := range discarded {
        if d.Proxy != nil {
            key := buildProxyKey(d.Proxy)
            discardedMap[key] = true
        }
    }

    var result []map[string]any
    removedCount := 0
    for _, proxy := range proxies {
        key := buildProxyKey(proxy)
        if !discardedMap[key] {
            result = append(result, proxy)
        } else {
            removedCount++
        }
    }
    if removedCount > 0 {
        slog.Info(fmt.Sprintf("已移除 %d 个上次丢弃的节点", removedCount))
    }
    return result
}

// buildProxyKey 根据代理的关键信息构建唯一标识
func buildProxyKey(proxy map[string]any) string {
    server, _ := proxy["server"].(string)
    port := proxy["port"]
    servername, _ := proxy["servername"].(string)
    password, _ := proxy["password"].(string)
    if password == "" {
        password, _ = proxy["uuid"].(string)
    }
    sni, _ := proxy["sni"].(string)
    network, _ := proxy["network"].(string)
    return fmt.Sprintf("%s:%v:%s:%s:%s:%s", server, port, servername, password, sni, network)
}
