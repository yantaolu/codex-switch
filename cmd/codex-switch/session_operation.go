package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

type sessionOperationResult struct {
	ID  string
	Err error
}
type sessionOperation struct {
	Ctx                       context.Context
	Cancel                    context.CancelFunc
	Copy                      func(context.Context) (string, error)
	Started                   chan struct{}
	Done                      chan sessionOperationResult
	Result                    sessionOperationResult
	Finished, Canceling, Exit bool
}

func (m sessionOperation) Init() tea.Cmd {
	return func() tea.Msg {
		close(m.Started)
		var r sessionOperationResult
		if m.Ctx.Err() != nil {
			r.Err = m.Ctx.Err()
		} else {
			r.ID, r.Err = m.Copy(m.Ctx)
		}
		m.Done <- r
		return r
	}
}
func (m sessionOperation) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch k := msg.(type) {
	case sessionOperationResult:
		m.Result = k
		m.Finished = true
		return m, tea.Quit
	case tea.KeyMsg:
		switch k.String() {
		case "esc":
			m.Canceling = true
			m.Cancel()
		case "ctrl+c", "q", "Q":
			m.Exit = true
			m.Canceling = true
			m.Cancel()
		}
	}
	return m, nil
}
func (m sessionOperation) View() string {
	if m.Finished {
		return ""
	}
	status := "正在创建接续副本"
	if m.Canceling {
		status = "正在取消并清理进程"
	}
	return "\n" + titleStyle.Render("会话接续") + "\n\n" + mutedStyle.Render(status) + "\n\n" + mutedStyle.Render("Esc 返回  |  Ctrl+C 退出") + "\n"
}
func runSessionOperation(copy func(context.Context) (string, error)) (string, error) {
	return runSessionOperationWithOptions(copy)
}
func runSessionOperationWithOptions(copy func(context.Context) (string, error), options ...tea.ProgramOption) (string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := sessionOperation{Ctx: ctx, Cancel: cancel, Copy: copy, Started: make(chan struct{}), Done: make(chan sessionOperationResult, 1)}
	result, err := tea.NewProgram(m, append([]tea.ProgramOption{tea.WithAltScreen()}, options...)...).Run()
	cancel()
	if err != nil {
		select {
		case <-m.Started:
			<-m.Done
		default:
		}
		return "", errors.New("会话接续界面启动失败")
	}
	final := result.(sessionOperation)
	<-m.Done
	if final.Exit {
		if final.Result.ID != "" {
			fmt.Fprintln(os.Stderr, "接续副本已创建：", final.Result.ID)
		}
		if final.Result.Err != nil && (!errors.Is(final.Result.Err, context.Canceled) || isUnconfirmedCopy(final.Result.Err)) {
			fmt.Fprintln(os.Stderr, final.Result.Err)
		}
		return "", errExit
	}
	if final.Result.ID != "" {
		return final.Result.ID, final.Result.Err
	}
	if errors.Is(final.Result.Err, context.Canceled) && !isUnconfirmedCopy(final.Result.Err) {
		return "", errBack
	}
	return "", final.Result.Err
}
