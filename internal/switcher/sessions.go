package switcher

import (
	"bufio"
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

type Session struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Provider string    `json:"provider"`
	Updated  time.Time `json:"updated"`
}

var sessionID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type SessionCopyUnconfirmedError struct{ Cause error }

func (e *SessionCopyUnconfirmedError) Error() string {
	return "复制请求已发送，但结果未确认；可能已生成副本，请先查看会话列表，避免重复复制"
}
func (e *SessionCopyUnconfirmedError) Unwrap() error { return e.Cause }

type SessionPage struct {
	Items         []Session
	Total         int
	Skipped       int
	Truncated     bool
	TitlesPartial bool
}
type sessionHeap []Session

func (h sessionHeap) Len() int { return len(h) }
func (h sessionHeap) Less(i, j int) bool {
	if h[i].Updated.Equal(h[j].Updated) {
		return h[i].ID > h[j].ID
	}
	return h[i].Updated.Before(h[j].Updated)
}
func (h sessionHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *sessionHeap) Push(v any)   { *h = append(*h, v.(Session)) }
func (h *sessionHeap) Pop() any     { old := *h; n := len(old); v := old[n-1]; *h = old[:n-1]; return v }

var foundSession = errors.New("session located")

// Metadata scan retains only one page plus its offset; it never loads history.
func (e Engine) sessionPage(ctx context.Context, exclude, id string, offset, limit int) (SessionPage, error) {
	page := SessionPage{Items: []Session{}}
	if offset < 0 || offset > 10000 || limit < 1 || limit > 200 {
		return page, errors.New("会话页大小须为 1–200，偏移须为 0–10000")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	retained := &sessionHeap{}
	heap.Init(retained)
	err := filepath.WalkDir(filepath.Join(e.Home, "sessions"), func(path string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		// Descriptor validation closes the symlink race without reading entire rollouts.
		fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(fd), path)
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			page.Skipped++
			return nil
		}
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 4096), 1<<20)
		var record struct {
			Type    string `json:"type"`
			Payload struct {
				ID       string          `json:"id"`
				Provider string          `json:"model_provider"`
				Source   json.RawMessage `json:"source"`
			} `json:"payload"`
		}
		valid := scan.Scan() && json.Unmarshal(scan.Bytes(), &record) == nil && record.Type == "session_meta" && sessionID.MatchString(record.Payload.ID)
		f.Close()
		if !valid {
			page.Skipped++
			return nil
		}
		var source string
		json.Unmarshal(record.Payload.Source, &source)
		if source != "cli" && source != "vscode" && source != "appServer" {
			page.Skipped++
			return nil
		}
		if exclude != "" && record.Payload.Provider == exclude {
			return nil
		}
		item := Session{ID: record.Payload.ID, Provider: record.Payload.Provider, Updated: info.ModTime()}
		if id != "" {
			if item.ID == id {
				page.Items = []Session{item}
				return foundSession
			}
			return nil
		}
		page.Total++
		heap.Push(retained, item)
		if retained.Len() > offset+limit {
			heap.Pop(retained)
		}
		return nil
	})
	if errors.Is(err, foundSession) {
		return page, nil
	}
	if os.IsNotExist(err) {
		return page, nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		page.Truncated = true
	} else if err != nil {
		if ctx.Err() != nil {
			return page, ctx.Err()
		}
		return page, errors.New("无法读取本地会话列表")
	}
	sort.Slice(*retained, func(i, j int) bool {
		a, b := (*retained)[i], (*retained)[j]
		if a.Updated.Equal(b.Updated) {
			return a.ID < b.ID
		}
		return a.Updated.After(b.Updated)
	})
	if offset < retained.Len() {
		page.Items = append(page.Items, (*retained)[offset:]...)
	}
	page.TitlesPartial = readSessionTitles(ctx, e.Home, page.Items)
	return page, nil
}
func (e Engine) Sessions() ([]Session, error) {
	page, err := e.sessionPage(context.Background(), "", "", 0, 200)
	return page.Items, err
}
func (e Engine) activeProvider() (string, error) {
	st, err := e.state()
	if err != nil {
		return "", err
	}
	p, ok := st.Profiles[st.Current]
	if !ok {
		return "", errors.New("请先选择当前账户")
	}
	expected := "openai"
	if p.Kind == "provider" {
		expected = providerID
	}
	live, err := snapshot(e.Home)
	if err != nil {
		return "", err
	}
	route, err := Route(live.Config)
	if err != nil {
		return "", err
	}
	current, _ := route["model_provider"].(string)
	if current == "" {
		current = "openai"
	}
	if current != expected {
		return "", errors.New("当前配置与账户不一致，请先重新切换账户")
	}
	return expected, nil
}
func (e Engine) SessionCandidatesPage(ctx context.Context, offset, limit int) (SessionPage, error) {
	provider, err := e.activeProvider()
	if err != nil {
		return SessionPage{}, err
	}
	return e.sessionPage(ctx, provider, "", offset, limit)
}
func (e Engine) SessionCandidates() ([]Session, error) {
	page, err := e.SessionCandidatesPage(context.Background(), 0, 200)
	return page.Items, err
}

// Native fork preserves the source and assigns the active account's provider to
// a new local thread. It never sends a model request or rewrites rollout files.
func (e Engine) ContinueSession(ctx context.Context, id string) (newID string, result error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !sessionID.MatchString(id) {
		return "", errors.New("会话 ID 格式无效")
	}
	unlock, err := e.Store.Lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	if err = e.checkPending(); err != nil {
		return "", err
	}
	st, err := e.state()
	if err != nil {
		return "", err
	}
	p, ok := st.Profiles[st.Current]
	if !ok {
		return "", errors.New("请先切换到要接续会话的账户")
	}
	provider := "openai"
	if p.Kind == "provider" {
		provider = providerID
	}
	live, err := snapshot(e.Home)
	if err != nil {
		return "", err
	}
	route, err := Route(live.Config)
	if err != nil {
		return "", err
	}
	configured, _ := route["model_provider"].(string)
	if configured == "" {
		configured = "openai"
	}
	if configured != provider {
		return "", errors.New("当前配置与账户状态不一致，请先重新切换账户")
	}
	if p.Kind == "chatgpt" {
		currentID, currentErr := AuthIdentity(live.Auth)
		savedID, savedErr := AuthIdentity(p.Auth)
		if currentErr != nil || savedErr != nil || currentID != savedID {
			return "", errors.New("当前登录与账户状态不一致，请先重新切换账户")
		}
	} else {
		var auth map[string]any
		if p.Provider == nil || json.Unmarshal(live.Auth, &auth) != nil || auth["OPENAI_API_KEY"] != p.Provider.APIKey {
			return "", errors.New("当前认证与账户状态不一致，请先重新切换账户")
		}
	}
	page, err := e.sessionPage(ctx, "", id, 0, 1)
	if err != nil {
		return "", err
	}
	found := false
	for _, s := range page.Items {
		if s.ID == id {
			if s.Provider == provider {
				return "", errors.New("供应商一致，无需接续；请直接打开原会话")
			}
			found = true
			break
		}
	}
	if !found {
		return "", errors.New("本地会话不存在")
	}
	runtime, ok := e.Runtime.(MacRuntime)
	if !ok {
		return "", errors.New("当前环境不支持原生会话接续")
	}
	event := AuditEvent{Operation: "session_fork", ID: auditID(), From: st.Current, To: st.Current, Status: "started", SourceSession: id}
	if err = e.Store.appendAudit(event); err != nil {
		return "", err
	}
	defer func() {
		event.TargetSession = newID
		if logErr := e.finishAudit(event, "session_fork", result); logErr != nil {
			if result == nil {
				result = &OperationError{Committed: true, Cause: logErr}
			} else {
				result = errors.Join(result, logErr)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	client, err := startNative(ctx, runtime.Codex, e.Home, true)
	if err != nil {
		return "", err
	}
	defer client.Close()
	rpc := &client.rpc
	params := map[string]any{"threadId": id, "modelProvider": provider, "excludeTurns": true}
	if model, ok := route["model"].(string); ok && model != "" {
		params["model"] = model
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	raw, err := rpc.call("thread/fork", params)
	if err != nil {
		var rejected *RPCRejectedError
		if errors.As(err, &rejected) {
			return "", errors.New("会话接续请求被拒绝；原会话未修改")
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return "", &SessionCopyUnconfirmedError{Cause: err}
	}
	var reply struct {
		Thread struct {
			ID       string `json:"id"`
			Provider string `json:"modelProvider"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &reply) != nil || !sessionID.MatchString(reply.Thread.ID) || reply.Thread.ID == id || reply.Thread.Provider != provider {
		return "", &SessionCopyUnconfirmedError{Cause: errors.New("会话接口返回结果不符合预期")}
	}
	return reply.Thread.ID, nil
}
