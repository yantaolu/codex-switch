package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"codex-switch/internal/switcher"
	tea "github.com/charmbracelet/bubbletea"
)

func usageSummary(u switcher.UsageSnapshot) string {
	if u.Error != "" && u.UpdatedAt.IsZero() {
		return "查询失败"
	}
	if len(u.Metrics) > 0 {
		m := u.Metrics[0]
		if m.Remaining != nil {
			suffix := ""
			if u.Error != "" {
				suffix = " · 旧数据"
			}
			return fmt.Sprintf("剩余 %.2f%s%s", *m.Remaining, m.Unit, suffix)
		}
	}
	if u.Plan != "" {
		return u.Plan
	}
	return "尚未查询"
}
func usageLines(u switcher.UsageSnapshot) string {
	lines := []string{}
	if u.Plan != "" {
		lines = append(lines, "套餐："+u.Plan)
	}
	if u.Status != "" {
		lines = append(lines, "状态："+u.Status)
	}
	for _, m := range u.Metrics {
		parts := []string{m.Name}
		if m.Remaining != nil {
			parts = append(parts, fmt.Sprintf("剩余 %.2f%s", *m.Remaining, m.Unit))
		}
		if m.Total != nil && m.Unit != "%" {
			parts = append(parts, fmt.Sprintf("总额 %.2f%s", *m.Total, m.Unit))
		}
		if m.Used != nil && m.Unit != "%" {
			parts = append(parts, fmt.Sprintf("已用 %.2f%s", *m.Used, m.Unit))
		}
		if !m.ResetAt.IsZero() {
			parts = append(parts, "重置 "+m.ResetAt.Local().Format("01-02 15:04"))
		}
		lines = append(lines, strings.Join(parts, " · "))
	}
	if !u.ExpiresAt.IsZero() {
		lines = append(lines, "到期："+u.ExpiresAt.Local().Format("2006-01-02 15:04"))
	}
	if !u.UpdatedAt.IsZero() {
		label := "更新："
		if u.Error != "" {
			label = "上次成功："
		}
		lines = append(lines, label+u.UpdatedAt.Local().Format("01-02 15:04:05"))
	}
	if u.Error != "" {
		lines = append(lines, "查询失败："+u.Error)
	}
	if len(lines) == 0 {
		lines = append(lines, "尚未查询")
	}
	return strings.Join(lines, "\n")
}

type usageResultMsg struct {
	Name     string
	Snapshot switcher.UsageSnapshot
	Err      error
}
type usageMenu struct {
	menuModel
	Engine      switcher.Engine
	Ctx         context.Context
	Names       []string
	Profiles    map[string]switcher.Profile
	BaseTitle   string
	BaseOptions []string
	Results     map[string]switcher.UsageSnapshot
	Queue       []string
	Force       bool
	Detail      bool
}

func newUsageMenu(ctx context.Context, e switcher.Engine, title string, options, names []string, profiles map[string]switcher.Profile, detail bool) usageMenu {
	menu := withReturn(menuModel{Title: title, Options: append([]string(nil), options...), Height: 24})
	if detail {
		menu.Cursor = len(menu.Options) - 1
	}
	m := usageMenu{menuModel: menu, Engine: e, Ctx: ctx, Names: names, Profiles: profiles, BaseTitle: title, BaseOptions: menu.Options, Results: map[string]switcher.UsageSnapshot{}, Detail: detail, Force: true}
	for _, name := range names {
		p := profiles[name]
		m.Results[name] = e.CachedUsage(name)
		if p.Kind == "chatgpt" || p.Usage != nil && p.Usage.Type != "off" {
			m.Queue = append(m.Queue, name)
		}
	}
	m.decorate()
	return m
}
func (m usageMenu) Init() tea.Cmd { return m.nextQuery() }
func (m usageMenu) nextQuery() tea.Cmd {
	if len(m.Queue) == 0 {
		return nil
	}
	name := m.Queue[0]
	force := m.Force
	return func() tea.Msg {
		u, err := m.Engine.QueryUsage(m.Ctx, name, force)
		return usageResultMsg{Name: name, Snapshot: u, Err: err}
	}
}
func (m *usageMenu) decorate() {
	m.Title = m.BaseTitle
	m.Options = append([]string(nil), m.BaseOptions...)
	if m.Detail && len(m.Names) > 0 {
		name := m.Names[0]
		p := m.Profiles[name]
		if p.Kind == "provider" && (p.Usage == nil || p.Usage.Type == "off") {
			m.Title += "\n未配置查询 · 使用额度配置启用"
		} else {
			m.Title += "\n" + usageLines(m.Results[name])
		}
	} else {
		for i, name := range m.Names {
			p := m.Profiles[name]
			text := "未配置查询"
			if p.Kind == "chatgpt" || p.Usage != nil && p.Usage.Type != "off" {
				text = usageSummary(m.Results[name])
			}
			m.Options[i] += " · " + text
		}
	}

}
func (m usageMenu) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if result, ok := msg.(usageResultMsg); ok {
		if result.Err != nil {
			result.Snapshot = m.Results[result.Name]
			result.Snapshot.Error = result.Err.Error()
		}
		m.Results[result.Name] = result.Snapshot
		if len(m.Queue) > 0 {
			m.Queue = m.Queue[1:]
		}
		m.decorate()
		return m, m.nextQuery()
	}
	if key, ok := msg.(tea.KeyMsg); ok && (key.String() == "r" || key.String() == "R") {
		if len(m.Queue) > 0 {
			return m, nil
		}
		m.Force = true
		for _, name := range m.Names {
			p := m.Profiles[name]
			if p.Kind == "chatgpt" || p.Usage != nil && p.Usage.Type != "off" {
				m.Queue = append(m.Queue, name)
			}
		}
		m.decorate()
		return m, m.nextQuery()
	}
	next, cmd := m.menuModel.Update(msg)
	m.menuModel = next.(menuModel)
	return m, cmd
}
func (m usageMenu) View() string {
	menu := m.menuModel
	menu.Refresh = true
	return menu.View()
}
func chooseUsage(e switcher.Engine, title string, options, names []string, profiles map[string]switcher.Profile, detail bool) (int, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newUsageMenu(ctx, e, title, options, names, profiles, detail)
	if detail {
		m.Force = true
	}
	result, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return 0, errors.New("额度界面启动失败")
	}
	final := result.(usageMenu)
	if final.Exit {
		return 0, errExit
	}
	if !final.Selected {
		return 0, errBack
	}
	if final.Cursor == len(final.Options)-1 {
		return 0, errBack
	}
	return final.Cursor, nil
}
func uiAccounts(e switcher.Engine) error {
	for {
		st, err := e.Store.Load()
		if err != nil {
			return err
		}
		names := []string{}
		for name := range st.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		labels := []string{}
		for _, name := range names {
			s := switcher.ProfileDisplay(name, st.Profiles[name]) + " · " + profileDescription(st.Profiles[name])
			if name == st.Current {
				s += " · 当前"
			}
			labels = append(labels, s)
		}
		labels = append(labels, "返回")
		i, err := chooseUsage(e, "账户列表 · 选择查看详情", labels, names, st.Profiles, false)
		if err != nil {
			return err
		}
		if i == len(names) {
			return errBack
		}
		if err = uiUsageDetail(e, names[i], false); errors.Is(err, errExit) {
			return err
		} else if err != nil && !errors.Is(err, errBack) {
			return err
		}
	}
}
func uiUsageDetail(e switcher.Engine, name string, status bool) error {
	for {
		st, err := e.Store.Load()
		if err != nil {
			return err
		}
		p, ok := st.Profiles[name]
		if !ok {
			return errors.New("账户不存在")
		}
		title := "账户详情 · " + switcher.ProfileLabel(name, p) + " · " + profileDescription(p)
		if p.Provider != nil {
			title += "\nAPI：" + p.Provider.BaseURL
		}
		if status {
			title = "当前状态 · " + switcher.ProfileLabel(name, p) + " · " + profileDescription(p)
			if diagnostic, er := e.Inspect(); er == nil {
				title += fmt.Sprintf("\n认证一致：%v · 待恢复：%v", diagnostic.AuthMatches, diagnostic.Pending)
			} else {
				title += "\n核查失败：" + er.Error()
			}
		}
		title += "\n" + switcher.CredentialStatus(p)
		options := []string{"备注编辑"}
		if p.Kind == "provider" {
			options = append(options, "额度配置", "服务编辑")
		} else {
			options = append(options, "重新登录")
		}
		options = append(options, "返回")
		i, err := chooseUsage(e, title, options, []string{name}, st.Profiles, true)
		if err != nil {
			return err
		}
		switch {
		case i == 0:
			remark, er := input("账户备注", switcher.ProfileLabel(name, p), false, switcher.ValidateRemark)
			if er != nil {
				err = er
			} else if remark != switcher.ProfileLabel(name, p) {
				err = e.SetRemark(name, remark)
			}
		case p.Kind == "provider" && i == 2:
			err = editProviderUI(e, name, *p.Provider)
		case p.Kind == "provider":
			err = uiUsageConfig(e, name, p)
		default:
			err = runAccountSwitch(switcher.ProfileLabel(name, p), func() error {
				return interruptedOperation(e, func(engine switcher.Engine) error { return engine.Relogin(name) })
			}, chooseMenu)
		}
		if err != nil && !errors.Is(err, errBack) {
			return err
		}
	}
}
func uiUsageConfig(e switcher.Engine, name string, p switcher.Profile) error {
	i, err := chooseMenu(menuModel{Title: "额度配置 · " + name, Options: []string{"Sub2API", "通用模板", "关闭查询", "返回"}, Cursor: 3, Height: 24})
	if err != nil {
		return err
	}
	if i == 3 {
		return errBack
	}
	if i == 2 {
		return e.UpdateUsage(name, switcher.UsageConfig{Type: "off"})
	}
	kind := "sub2api"
	if i == 1 {
		kind = "generic"
	}
	c := switcher.DefaultUsage(p, kind)
	if p.Usage != nil && p.Usage.Type == kind {
		c = *p.Usage
	}
	if kind == "sub2api" {
		initial := c.URL
		if initial == "" && p.Provider != nil {
			initial = switcher.Sub2APIUsageURL(p.Provider.BaseURL)
		}
		endpoint, er := input("Sub2API 查询地址（复用账户 API Key）", initial, false, func(s string) error { return switcher.ValidateUsage(switcher.UsageConfig{Type: "sub2api", URL: s}) })
		if er != nil {
			return er
		}
		c.URL = endpoint
		return e.UpdateUsage(name, c)
	}
	return editUsageJSON(e, name, c)
}

// Reuse the established plaintext textarea, with JSON validation and a fixed name.
type usageEditor struct {
	providerEditor
	Engine  switcher.Engine
	Account string
}

func (m usageEditor) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		if m.Saving {
			next, cmd := m.providerEditor.Update(msg)
			m.providerEditor = next.(providerEditor)
			return m, cmd
		}
		if key.String() == "tab" || key.String() == "shift+tab" {
			return m, nil
		}
		if key.String() == "ctrl+s" {
			if m.Pending {
				return m, nil
			}
			c, err := decodeUsage([]byte(m.Config.Value()))
			if err != nil {
				m.Error = err.Error()
				return m, nil
			}
			m.Saving = true
			m.Error = ""
			return m, m.Saves.start(func(ctx context.Context) error {
				return m.Engine.WithContext(ctx).UpdateUsage(m.Account, c)
			})
		}
	}
	next, cmd := m.providerEditor.Update(msg)
	m.providerEditor = next.(providerEditor)
	return m, cmd
}
func (m usageEditor) View() string {
	if !m.Saving && (m.Done || m.Canceled || m.Exit) {
		return ""
	}
	state := ""
	if m.Saving {
		state = "保存中…"
		if m.Canceling {
			state = "正在取消并等待清理…"
		}
	}
	return "\n" + titleStyle.Render("通用模板 · "+m.Account) + "\n\n" + m.Config.View() + "\n" + errorStyle.Render(safeUI(m.Error)) + "\n" + mutedStyle.Render("字段路径：data.balance 或 /data/balance；{{apiKey}} 复用账户密钥\nCtrl+S 保存 · Esc 返回 · "+state) + "\n"
}
func decodeUsage(b []byte) (switcher.UsageConfig, error) {
	var c switcher.UsageConfig
	if len(b) > 1<<20 {
		return c, errors.New("查询配置过大")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return c, errors.New("查询配置 JSON 无效或包含未知字段")
	}
	if d.Decode(new(any)) != io.EOF {
		return c, errors.New("查询配置只能包含一个 JSON 对象")
	}
	return c, switcher.ValidateUsage(c)
}
func editUsageJSON(e switcher.Engine, name string, c switcher.UsageConfig) error {
	b, _ := json.MarshalIndent(c, "", "  ")
	editor := newProviderEditor(nil)
	editor.Config.SetValue(string(b))
	result, err := runEditorProgram(usageEditor{providerEditor: editor, Engine: e, Account: name}, editor.Saves)
	if err != nil {
		return err
	}
	m := result.(usageEditor)
	return editorResult(m.providerEditor)
}
