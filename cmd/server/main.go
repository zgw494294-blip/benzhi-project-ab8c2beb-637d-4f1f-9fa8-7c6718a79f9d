package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/redaction"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/store"
	webapp "benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/web"
	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/workflow"
)

const defaultAddress = "127.0.0.1:19081"

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(arguments []string) error {
	defaultAddr, err := addressFromEnvironment(os.Getenv("PORT"))
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("oral-history-release-desk", flag.ContinueOnError)
	addr := flags.String("addr", defaultAddr, "HTTP 监听地址（仅允许回环地址）")
	dataDir := flags.String("data", "data", "本地 JSON 数据目录")
	selfcheck := flags.Bool("selfcheck", false, "通过真实 HTTP 链路执行有界全流程自检后退出")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("存在无法识别的参数: %s", strings.Join(flags.Args(), " "))
	}
	if !flagProvided(arguments, "addr") {
		*addr = defaultAddr
	}
	if err := validateAddress(*addr); err != nil {
		return err
	}
	if *selfcheck {
		return runSelfcheck(*addr)
	}
	repository, err := store.Open(*dataDir)
	if err != nil {
		return err
	}
	handler := webapp.New(workflow.New(repository, redaction.New(), time.Now)).Handler()
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", *addr, err)
	}
	server := configuredServer(handler)
	log.Printf("口述史授权净化发布台已监听 http://%s", listener.Addr())
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case serveErr := <-errCh:
		if !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("优雅关闭 HTTP 服务: %w", err)
		}
		return nil
	}
}

func addressFromEnvironment(portValue string) (string, error) {
	if strings.TrimSpace(portValue) == "" {
		return defaultAddress, nil
	}
	port, err := strconv.Atoi(portValue)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("PORT 必须是 1 到 65535 之间的端口号")
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), nil
}

func flagProvided(arguments []string, name string) bool {
	prefix := "-" + name
	for i, value := range arguments {
		if value == prefix || strings.HasPrefix(value, prefix+"=") {
			return true
		}
		if value == "--"+name || strings.HasPrefix(value, "--"+name+"=") {
			return true
		}
		if i > 0 && arguments[i-1] == "--" {
			return false
		}
	}
	return false
}

func validateAddress(addr string) error {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("-addr 必须为 host:port: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("-addr 只允许明确的回环 IP，当前为 %q", host)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("-addr 端口必须在 1 到 65535 之间")
	}
	return nil
}

func configuredServer(handler http.Handler) *http.Server {
	return &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
}

func runSelfcheck(addr string) error {
	root, err := os.MkdirTemp("", "oral-history-selfcheck-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	repository, err := store.Open(root)
	if err != nil {
		return err
	}
	service := workflow.New(repository, redaction.New(), time.Now)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("自检监听 %s: %w", addr, err)
	}
	server := configuredServer(webapp.New(service).Handler())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	baseURL := "http://" + listener.Addr().String()
	client := &http.Client{Timeout: 4 * time.Second}
	checkCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := executeHTTPFlow(checkCtx, client, baseURL); err != nil {
		_ = server.Close()
		return fmt.Errorf("HTTP 全流程自检失败: %w", err)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if serveErr := <-serveDone; !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	fmt.Println("自检通过：真实 HTTP 链路完成建档、授权、转写、标注、脱敏、复核、冻结、批准与清单验证")
	return nil
}

func executeHTTPFlow(ctx context.Context, client *http.Client, baseURL string) error {
	var value archive.InterviewArchive
	if err := requestJSON(ctx, client, http.MethodGet, baseURL+"/healthz", nil, nil); err != nil {
		return err
	}
	create := map[string]any{"id": "selfcheck-archive", "subjectCode": "SC-001", "interviewDate": "2024-05-20", "purpose": "馆藏研究展示", "curator": "自检整理员", "actionKey": "sc-create"}
	if err := requestJSON(ctx, client, http.MethodPost, baseURL+"/api/archives", create, &value); err != nil {
		return err
	}
	consent := map[string]any{"expectedVersion": value.Version, "allowedUses": []string{"馆内研究", "非商业展陈"}, "restrictedTopics": []string{"家庭医疗史"}, "nameDisclosure": "pseudonym_only", "sealedUntil": "2020-12-31", "recordedBy": "自检整理员", "actionKey": "sc-consent"}
	if err := requestJSON(ctx, client, http.MethodPut, baseURL+"/api/archives/selfcheck-archive/consent", consent, &value); err != nil {
		return err
	}
	segment := map[string]any{"expectedVersion": value.Version, "expectedRevision": 0, "speakerCode": "SC-001", "sourceText": "我住在北京市东城区，电话13800138000。", "actionKey": "sc-segment"}
	if err := requestJSON(ctx, client, http.MethodPut, baseURL+"/api/archives/selfcheck-archive/segments/S001", segment, &value); err != nil {
		return err
	}
	marks := []map[string]any{
		{"expectedVersion": value.Version, "id": "mark-location", "segmentId": "S001", "startOffset": 3, "endOffset": 9, "category": "precise_location", "strategy": "generalize", "replacement": "北京市某城区", "actionKey": "sc-mark-1"},
		{"id": "mark-contact", "segmentId": "S001", "startOffset": 12, "endOffset": 23, "category": "contact", "strategy": "replace", "replacement": "[联系方式已隐去]", "actionKey": "sc-mark-2"},
	}
	if err := requestJSON(ctx, client, http.MethodPost, baseURL+"/api/archives/selfcheck-archive/marks", marks[0], &value); err != nil {
		return err
	}
	marks[1]["expectedVersion"] = value.Version
	if err := requestJSON(ctx, client, http.MethodPost, baseURL+"/api/archives/selfcheck-archive/marks", marks[1], &value); err != nil {
		return err
	}
	if err := versionAction(ctx, client, baseURL+"/api/archives/selfcheck-archive/submit", value.Version, "sc-submit", &value); err != nil {
		return err
	}
	if err := versionAction(ctx, client, baseURL+"/api/archives/selfcheck-archive/redaction", value.Version, "sc-redact", &value); err != nil {
		return err
	}
	for index, markID := range []string{"mark-location", "mark-contact"} {
		review := map[string]any{"expectedVersion": value.Version, "markId": markID, "approved": true, "reason": "", "actionKey": fmt.Sprintf("sc-review-%d", index)}
		if err := requestJSON(ctx, client, http.MethodPost, baseURL+"/api/archives/selfcheck-archive/reviews", review, &value); err != nil {
			return err
		}
	}
	if err := versionAction(ctx, client, baseURL+"/api/archives/selfcheck-archive/freeze", value.Version, "sc-freeze", &value); err != nil {
		return err
	}
	var released map[string]any
	approval := map[string]any{"expectedVersion": value.Version, "approvedBy": "自检发布负责人", "actionKey": "sc-approve"}
	if err := requestJSON(ctx, client, http.MethodPost, baseURL+"/api/archives/selfcheck-archive/approve", approval, &released); err != nil {
		return err
	}
	manifestData, err := json.Marshal(released)
	if err != nil {
		return err
	}
	var verification struct {
		Valid bool `json:"valid"`
	}
	if err := requestRaw(ctx, client, http.MethodPost, baseURL+"/api/manifests/verify", manifestData, &verification); err != nil {
		return err
	}
	if !verification.Valid {
		return errors.New("清单验证入口未返回 valid=true")
	}
	var view workflow.ArchiveView
	if err := requestJSON(ctx, client, http.MethodGet, baseURL+"/api/archives/selfcheck-archive", nil, &view); err != nil {
		return err
	}
	if view.Archive.Status != archive.StatusPublished || view.Manifest == nil {
		return errors.New("自检档案未完成发布")
	}
	return nil
}

func versionAction(ctx context.Context, client *http.Client, url string, version int64, key string, output any) error {
	return requestJSON(ctx, client, http.MethodPost, url, map[string]any{"expectedVersion": version, "actionKey": key}, output)
}

func requestJSON(ctx context.Context, client *http.Client, method, url string, input, output any) error {
	var data []byte
	if input != nil {
		var err error
		data, err = json.Marshal(input)
		if err != nil {
			return err
		}
	}
	return requestRaw(ctx, client, method, url, data, output)
}

func requestRaw(ctx context.Context, client *http.Client, method, url string, data []byte, output any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("X-Actor", "自检操作员")
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 3<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%s %s 返回 %d: %s", method, url, response.StatusCode, body)
	}
	if output != nil && len(body) > 0 {
		if err := json.Unmarshal(body, output); err != nil {
			return fmt.Errorf("解析 %s 响应: %w", url, err)
		}
	}
	return nil
}
