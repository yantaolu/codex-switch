package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"codex-switch/internal/switcher"
)

func sessionCommand(e switcher.Engine, args []string) error {
	f := flag.NewFlagSet("sessions", flag.ContinueOnError)
	limit := f.Int("limit", 100, "会话页大小 1–200")
	offset := f.Int("offset", 0, "会话偏移 0–10000")
	if err := f.Parse(args); err != nil {
		return err
	}
	args = f.Args()
	if len(args) > 1 {
		return errors.New("用法：sessions [--limit N] [--offset N] [会话ID]")
	}
	if len(args) == 1 {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		id, err := e.ContinueSession(ctx, args[0])
		if err != nil {
			return err
		}
		fmt.Println("已使用当前账户创建接续副本：", id)
		fmt.Println("在桌面客户端重新打开会话列表，或运行：codex resume " + id)
		return nil
	}
	page, err := e.SessionCandidatesPage(context.Background(), *offset, *limit)
	if err != nil {
		return err
	}
	for _, s := range page.Items {
		fmt.Printf("%s  %-14s  %s  %s\n", s.Updated.Local().Format("01-02 15:04"), safeUI(s.Provider), s.ID, sessionTitle(s))
	}
	fmt.Printf("本页 %d / %d 条候选；跳过 %d 条非本地或无法识别记录\n", len(page.Items), page.Total, page.Skipped)
	if page.Truncated {
		fmt.Println("扫描达到时间上限，结果不完整；可缩小会话目录后重试。")
	}
	if page.TitlesPartial {
		fmt.Println("部分会话标题未读取；已优先读取最新索引，仍可通过会话 ID 识别。")
	}
	return nil
}

func uiSessions(e switcher.Engine) error {
	offset := 0
	const pageSize = 20
	for {
		page, err := e.SessionCandidatesPage(context.Background(), offset, pageSize)
		list := page.Items
		if err != nil {
			return err
		}
		if len(list) == 0 {
			title := "暂无接续候选 · 同供应商可直接打开原会话"
			if page.Skipped > 0 {
				title += fmt.Sprintf("\n跳过 %d 条非本地或无法识别记录", page.Skipped)
			}
			if page.Truncated {
				title += "\n扫描未完整，可重新进入列表"
			}
			_, err := choose(title, []string{"返回"})
			return err
		}
		labels := []string{}
		for _, s := range list {
			labels = append(labels, sessionLabel(s))
		}
		prev, next := -1, -1
		if offset > 0 {
			prev = len(labels)
			labels = append(labels, "上一页")
		}
		if offset+pageSize < page.Total && offset+pageSize <= 10000 {
			next = len(labels)
			labels = append(labels, "下一页")
		}
		labels = append(labels, "返回")
		title := fmt.Sprintf("会话接续 · 第 %d 页 · %d 条候选", offset/pageSize+1, page.Total)
		if page.Skipped > 0 {
			title += fmt.Sprintf("\n跳过 %d 条非本地或无法识别记录", page.Skipped)
		}
		if page.Truncated {
			title += " · 扫描未完整"
		}
		if page.TitlesPartial {
			title += "\n部分标题未读取，优先显示最新索引"
		}
		i, err := chooseMenu(menuModel{Title: title, Options: labels, Cursor: len(labels) - 1, Height: 24})
		if err != nil {
			return err
		}
		if i == len(labels)-1 {
			return errBack
		}
		if i == prev {
			offset = max(0, offset-pageSize)
			continue
		}
		if i == next {
			offset += pageSize
			continue
		}
		for {
			id, forkErr := runSessionOperation(func(ctx context.Context) (string, error) { return e.ContinueSession(ctx, list[i].ID) })
			if switcher.IsCommitted(forkErr) && id != "" {
				_, err := choose("副本已创建 · "+id+"\n"+forkErr.Error(), []string{"返回"})
				return err
			}
			if forkErr != nil {
				if errors.Is(forkErr, errBack) {
					break
				}
				if errors.Is(forkErr, errExit) {
					return forkErr
				}
				if isUnconfirmedCopy(forkErr) {
					_, showErr := chooseMenu(menuModel{Title: "接续结果未确认\n" + forkErr.Error(), Options: []string{"返回"}, Height: 24, ErrorTitle: true})
					if showErr != nil {
						return showErr
					}
					return errBack
				}
				choice, showErr := chooseMenu(menuModel{Title: "接续失败\n" + forkErr.Error(), Options: []string{"重试接续", "返回"}, Cursor: 1, Height: 24, ErrorTitle: true})
				if showErr != nil {
					return showErr
				}
				if choice != 0 {
					return errBack
				}
				continue
			}
			_, err = choose("接续副本 · "+id+"\n桌面客户端重新打开会话列表，或 codex resume "+id, []string{"返回"})
			if errors.Is(err, errBack) {
				break
			}
			return err
		}
	}
}

func sessionTitle(s switcher.Session) string {
	title := strings.TrimSpace(strings.Join(strings.Fields(safeUI(s.Title)), " "))
	if title == "" {
		return "未命名会话"
	}
	return title
}

func sessionLabel(s switcher.Session) string {
	id := s.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return s.Updated.Local().Format("01-02 15:04") + " · " + s.Provider + " · " + id + " · " + sessionTitle(s)
}

func isUnconfirmedCopy(err error) bool {
	var uncertain *switcher.SessionCopyUnconfirmedError
	return errors.As(err, &uncertain)
}

func switchLogLines(dir string) ([]string, error) {
	items, err := switcher.RecentAudit(dir)
	if err != nil {
		return nil, err
	}
	events := map[string]switcher.AuditEvent{}
	order := []string{}
	for _, e := range items {
		if _, exists := events[e.ID]; !exists {
			order = append(order, e.ID)
			if len(order) > 50 {
				delete(events, order[0])
				order = order[1:]
			}
		}
		events[e.ID] = e
	}
	lines := []string{}
	stages := map[string]string{"preflight": "配置检查", "stop": "退出应用", "authenticate": "登录认证", "write": "写入配置", "verify": "认证核验", "restart": "重启应用", "complete": "完成", "recover": "恢复操作", "session_fork": "复制会话", "config_cleanup": "完成"}
	for _, id := range order {
		e := events[id]
		status := "成功"
		if e.Status == "committed_warning" {
			status = "已提交 · 有提示"
		} else if e.Status == "pending" {
			status = "待恢复"
		} else if e.Status == "applied" {
			status = "已切换 · 启动未确认"
		} else if e.Status == "canceled" {
			status = "已取消"
		} else if e.Status == "unconfirmed" {
			status = "结果未确认"
		} else if e.Status == "started" {
			status = "未完成"
		} else if e.Status != "success" {
			status = "失败"
		}
		action := "切换"
		if e.Force {
			action = "强制切换"
		}
		if e.Operation == "recover" {
			action = "恢复"
		}
		if e.Operation == "config_cleanup" {
			action = "整理配置"
		}
		if e.Operation == "desktop_start" {
			action = "启动应用"
		}
		if e.Operation == "session_fork" {
			action = "接续"
		}
		if e.Operation == "update_models" {
			action = "模型修改"
		}
		if e.Operation == "update_provider" {
			action = "服务编辑"
		}
		stamp := e.Time
		if parsed, err := time.Parse(time.RFC3339Nano, e.Time); err == nil {
			stamp = parsed.Local().Format("01-02 15:04:05")
		}
		if e.RolledBack {
			status += " · 已回滚"
		}
		lines = append(lines, fmt.Sprintf("%s  %s %s → %s  %s · %s", stamp, action, e.From, e.To, status, stages[e.Stage]))
	}
	if len(lines) == 0 {
		lines = append(lines, "暂无切换记录")
	}
	return lines, nil
}
func printSwitchLogs(dir string) error {
	lines, err := switchLogLines(dir)
	if err != nil {
		return err
	}
	fmt.Println(strings.Join(lines, "\n"))
	return nil
}
func uiSwitchLogs(dir string) error {
	lines, err := switchLogLines(dir)
	if err != nil {
		return err
	}
	_, err = choose("切换日志", append(lines, "返回"))
	return err
}
