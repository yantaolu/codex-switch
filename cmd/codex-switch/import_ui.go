package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"codex-switch/internal/switcher"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type providerSavedMsg struct{ Err error }
type providerEditor struct {
	Pending                      bool
	SaveWarning                  error
	OperationErr                 error
	Canceling                    bool
	Saves                        *editorSaves
	Edit                         bool
	Name                         textinput.Model
	Config                       textarea.Model
	Focus                        int
	Saving, Done, Canceled, Exit bool
	Error                        string
	Provider                     switcher.Provider
	Warnings                     []string
	Save                         func(context.Context, string, switcher.Provider) error
}

func newProviderEditor(save func(context.Context, string, switcher.Provider) error) providerEditor {
	name := textinput.New()
	name.Placeholder = "例如 个人 / 备用中转（不是登录名）"
	name.CharLimit = 64
	name.Width = 60
	config := textarea.New()
	config.Placeholder = "粘贴完整 TOML 配置，然后使用方向键修改。\nTab 切换到账户备注，Ctrl+S 保存。"
	config.CharLimit = 0
	config.MaxHeight = 0
	config.SetWidth(76)
	config.SetHeight(14)
	config.ShowLineNumbers = true
	config.Prompt = " "
	config.FocusedStyle.CursorLine = lipgloss.NewStyle()
	config.FocusedStyle.CursorLineNumber = titleStyle
	config.FocusedStyle.LineNumber = mutedStyle
	config.FocusedStyle.Placeholder = mutedStyle
	config.FocusedStyle.EndOfBuffer = lipgloss.NewStyle()
	config.BlurredStyle = config.FocusedStyle
	config.Focus()
	return providerEditor{Name: name, Config: config, Focus: 1, Save: save, Saves: &editorSaves{}}
}
func (m providerEditor) Init() tea.Cmd { return textarea.Blink }
func (m providerEditor) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch k := msg.(type) {
	case tea.WindowSizeMsg:
		width := k.Width - 8
		if width < 20 {
			width = 20
		}
		m.Config.SetWidth(width)
		m.Name.Width = width - 4
		height := k.Height - 13
		if height < 3 {
			height = 3
		}
		m.Config.SetHeight(height)
	case providerSavedMsg:
		m.Saving = false
		if switcher.IsCommitted(k.Err) {
			m.SaveWarning = k.Err
			m.Done = true
			return m, tea.Quit
		}
		if switcher.NeedsRecovery(k.Err) || switcher.IsUnconfirmed(k.Err) {
			m.Pending = true
		}
		if k.Err != nil {
			m.Error = k.Err.Error()
			if m.Canceling {
				if !errors.Is(k.Err, context.Canceled) || m.Pending {
					m.OperationErr = k.Err
				}
				m.Canceled = !m.Exit
				return m, tea.Quit
			}
			return m, nil
		}
		m.Done = true
		return m, tea.Quit
	case tea.KeyMsg:
		if m.Saving {
			if k.String() == "esc" || k.String() == "ctrl+c" {
				m.Canceling = true
				m.Exit = m.Exit || k.String() == "ctrl+c"
				m.Saves.cancel()
			}
			return m, nil
		}
		switch k.String() {
		case "ctrl+c":
			m.Exit = true
			return m, tea.Quit
		case "esc":
			m.Canceled = true
			return m, tea.Quit
		case "tab", "shift+tab":
			if m.Edit {
				return m, nil
			}
			m.Focus = 1 - m.Focus
			if m.Focus == 0 {
				m.Config.Blur()
				return m, m.Name.Focus()
			}
			m.Name.Blur()
			return m, m.Config.Focus()
		case "ctrl+s":
			if m.Pending {
				return m, nil
			}
			name := strings.TrimSpace(m.Name.Value())
			if err := switcher.ValidateRemark(name); err != nil {
				m.Error = err.Error()
				m.Focus = 0
				m.Config.Blur()
				return m, m.Name.Focus()
			}
			p, warnings, err := switcher.ImportTOML([]byte(m.Config.Value()))
			if err != nil {
				m.Error = err.Error()
				return m, nil
			}
			m.Provider = p
			m.Warnings = warnings
			m.Error = ""
			m.OperationErr = nil
			m.Saving = true
			save := m.Save
			return m, m.Saves.start(func(ctx context.Context) error {
				if save == nil {
					return nil
				}
				return save(ctx, name, p)
			})
		}
	}
	var cmd tea.Cmd
	if m.Focus == 0 {
		m.Name, cmd = m.Name.Update(msg)
	} else {
		m.Config, cmd = m.Config.Update(msg)
	}
	return m, cmd
}
func (m providerEditor) View() string {
	if !m.Saving && (m.Done || m.Canceled || m.Exit) {
		return ""
	}
	nameLabel := mutedStyle.Render("账户备注")
	if m.Edit {
		nameLabel = mutedStyle.Render("账户 ID")
	}
	configLabel := mutedStyle.Render("TOML 配置")
	if m.Focus == 0 {
		nameLabel = titleStyle.Render("账户备注")
	} else {
		configLabel = titleStyle.Render("TOML 配置")
	}
	status := mutedStyle.Render("粘贴后可编辑，明文显示 API key。保存只添加账户，不会切换。")
	if m.Edit {
		status = mutedStyle.Render("Ctrl+S 保存修改；当前账户会通过切换事务更新，请先退出客户端。")
	}
	if m.Error != "" {
		status = errorStyle.Render(safeUI(m.Error))
	}
	if m.Saving {
		status = currentStyle.Render("正在保存账户…")
		if m.Canceling {
			status = mutedStyle.Render("正在取消并等待清理…")
		}
	}
	return fmt.Sprintf("\n%s\n\n%s  %s\n\n%s\n%s\n%s\n\n%s\n", titleStyle.Render("第三方配置"), nameLabel, m.Name.View(), configLabel, m.Config.View(), status, mutedStyle.Render("Tab 切换字段 · ↑↓←→ 编辑 · Ctrl+S 保存 · Esc 返回 · Ctrl+C 退出"))
}
func addProviderUI(o uiOptions) error {
	save := func(ctx context.Context, name string, p switcher.Provider) error {
		e, err := o.engine(true)
		if err != nil {
			return err
		}
		return e.WithContext(ctx).AddProvider(name, p)
	}
	return runProviderEditor("", save, o.Notice)
}
func runProviderEditor(initial string, save func(context.Context, string, switcher.Provider) error, notice func(string)) error {
	editor := newProviderEditor(save)
	editor.Name.SetValue(initial)
	result, err := runEditorProgram(editor, editor.Saves)
	if err != nil {
		return err
	}
	m := result.(providerEditor)
	if err := editorResult(m); err != nil {
		return err
	}
	if notice != nil {
		message := "已添加：" + strings.TrimSpace(m.Name.Value())
		if len(m.Warnings) > 0 {
			message += "\n  " + strings.Join(m.Warnings, "\n  ")
		}
		notice(message)
	}
	return nil
}

func runEditingProvider(editor providerEditor) error {
	result, err := runEditorProgram(editor, editor.Saves)
	if err != nil {
		return err
	}
	m := result.(providerEditor)
	return editorResult(m)
}
