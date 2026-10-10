package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"unicode"

	"codex-switch/internal/switcher"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

var errBack = errors.New("返回")
var errExit = errors.New("退出")

type uiOptions struct {
	Home, Store, App, Codex string
	Notice                  func(string)
}
type menuModel struct {
	Title      string
	Options    []string
	Cursor     int
	Selected   bool
	Canceled   bool
	Exit       bool
	Height     int
	Width      int
	Brand      bool
	Refresh    bool
	ErrorTitle bool
}

func (m menuModel) Init() tea.Cmd { return nil }
func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch k := msg.(type) {
	case tea.WindowSizeMsg:
		m.Height = k.Height
		m.Width = k.Width
	case tea.KeyMsg:
		switch k.String() {
		case "up", "k":
			if len(m.Options) > 0 {
				m.Cursor = (m.Cursor + len(m.Options) - 1) % len(m.Options)
			}
		case "down", "j":
			if len(m.Options) > 0 {
				m.Cursor = (m.Cursor + 1) % len(m.Options)
			}
		case "home":
			m.Cursor = 0
		case "end":
			if len(m.Options) > 0 {
				m.Cursor = len(m.Options) - 1
			}
		case "enter":
			if len(m.Options) > 0 {
				m.Selected = true
				return m, tea.Quit
			}
		case "esc":
			m.Canceled = true
			return m, tea.Quit
		case "ctrl+c", "q", "Q":
			m.Exit = true
			return m, tea.Quit
		}
	}
	return m, nil
}
func safeUI(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return -1
		}
		return r
	}, s)
}
func (m menuModel) View() string {
	if m.Selected || m.Canceled || m.Exit {
		return ""
	}
	var b strings.Builder
	titleLines := strings.Split(safeUI(m.Title), "\n")
	showBanner := m.Brand && m.Width >= 60 && m.Height >= 24
	maxTitle := m.Height - 10
	if showBanner {
		maxTitle -= len(mainBanner()) + 1
	}
	if maxTitle < 4 {
		maxTitle = 4
	}
	if len(titleLines) > maxTitle {
		titleLines = append(titleLines[:maxTitle-1], "… 部分信息省略，请增大终端窗口")
	}
	for i, line := range titleLines {
		if m.ErrorTitle {
			titleLines[i] = errorStyle.Render(strings.TrimSpace(line))
		} else if i == 0 {
			titleLines[i] = renderTitle(line)
		} else {
			titleLines[i] = mutedStyle.Render(strings.TrimSpace(line))
		}
	}
	if showBanner {
		titleLines[0] = renderTitle(strings.TrimPrefix(strings.Split(m.Title, "\n")[0], "codex-switch  "))
		titleLines = append(append(mainBanner(), ""), titleLines...)
	} else if m.Brand {
		titleLines[0] = projectLink("codex-switch") + "  " + renderTitle(strings.TrimPrefix(strings.Split(m.Title, "\n")[0], "codex-switch  "))
	}
	for i, line := range titleLines {
		if m.Width > 0 {
			titleLines[i] = ansi.Truncate(line, m.Width, "…")
		}
	}
	b.WriteString("\n" + strings.Join(titleLines, "\n") + "\n\n")
	n := m.Height - len(titleLines) - 6
	if n < 3 {
		n = 3
	}
	start := 0
	if m.Cursor >= n {
		start = m.Cursor - n + 1
	}
	end := start + n
	if end > len(m.Options) {
		end = len(m.Options)
	}
	if start > 0 {
		b.WriteString("  ↑ 更多\n")
	}
	for i := start; i < end; i++ {
		line := renderOption(m.Options[i], i == m.Cursor)
		if m.Width > 0 {
			line = ansi.Truncate(line, m.Width, "…")
		}
		fmt.Fprintf(&b, "%s\n", line)
	}
	if end < len(m.Options) {
		b.WriteString("  ↓ 更多\n")
	}
	footer := "↑↓  |  Enter 确认  |  Esc 返回  |  Q 退出"
	if m.Refresh {
		footer = "R 刷新  |  " + footer
	}
	if m.Width > 0 && ansi.StringWidth(footer) > m.Width {
		footer = "↑↓  Enter 确认  Esc 返回  Q 退出"
		if m.Refresh {
			footer = "R 刷新  " + footer
		}
		footer = ansi.Truncate(footer, m.Width, "…")
	}
	b.WriteString("\n" + mutedStyle.Render(footer) + "\n")
	return b.String()
}
func choose(title string, options []string) (int, error) {
	if len(options) == 0 {
		return 0, errors.New("没有可选择的项目")
	}
	return chooseMenu(menuModel{Title: title, Options: options, Height: 24})
}
func chooseMenu(menu menuModel) (int, error) {
	return chooseMenuWithOptions(menu)
}
func chooseMenuWithOptions(menu menuModel, options ...tea.ProgramOption) (int, error) {
	menu = withReturn(menu)
	p := tea.NewProgram(menu, append([]tea.ProgramOption{tea.WithAltScreen()}, options...)...)
	result, err := p.Run()
	if err != nil {
		return 0, errors.New("终端交互失败")
	}
	m := result.(menuModel)
	if m.Exit {
		return 0, errExit
	}
	if !m.Selected {
		return 0, errBack
	}
	if !m.Brand && m.Cursor == len(m.Options)-1 {
		return 0, errBack
	}
	return m.Cursor, nil
}

// Every child menu exposes the same final navigation item; action indexes stay stable.
func withReturn(m menuModel) menuModel {
	if !m.Brand && (len(m.Options) == 0 || m.Options[len(m.Options)-1] != "返回") {
		m.Options = append(append([]string(nil), m.Options...), "返回")
	}
	return m
}

type inputModel struct {
	Title                string
	Input                textinput.Model
	Done, Canceled, Exit bool
	Validate             func(string) error
	Error                string
	Secret               bool
}

func (m inputModel) Init() tea.Cmd { return textinput.Blink }
func (m inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			m.Canceled = true
			return m, tea.Quit
		case "ctrl+c":
			m.Exit = true
			return m, tea.Quit
		case "enter":
			if m.Validate != nil {
				if e := m.Validate(strings.TrimSpace(m.Input.Value())); e != nil {
					m.Error = e.Error()
					return m, nil
				}
			}
			m.Done = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.Input, cmd = m.Input.Update(msg)
	return m, cmd
}
func (m inputModel) View() string {
	if m.Done || m.Canceled || m.Exit {
		return ""
	}
	return "\n" + titleStyle.Render(safeUI(m.Title)) + "\n\n" + m.Input.View() + "\n\n" + errorStyle.Render(safeUI(m.Error)) + "\n" + mutedStyle.Render("Enter 确认 · Esc 返回 · Ctrl+C 退出") + "\n"
}
func input(title, initial string, secret bool, validate func(string) error) (string, error) {
	t := textinput.New()
	t.CharLimit = 4096
	t.Width = 70
	t.SetValue(initial)
	if secret {
		t.EchoMode = textinput.EchoPassword
		t.EchoCharacter = '•'
	}
	t.Focus()
	result, err := tea.NewProgram(inputModel{Title: title, Input: t, Validate: validate, Secret: secret}, tea.WithAltScreen()).Run()
	if err != nil {
		return "", errors.New("终端输入失败")
	}
	m := result.(inputModel)
	if m.Exit {
		return "", errExit
	}
	if !m.Done {
		return "", errBack
	}
	return strings.TrimSpace(m.Input.Value()), nil
}
func required(s string) error {
	if s == "" {
		return errors.New("此项不能为空")
	}
	return nil
}
func parseModels(s string) ([]switcher.Model, error) {
	models := []switcher.Model{}
	for _, id := range strings.Split(s, ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			models = append(models, switcher.Model{ID: id})
		}
	}
	if len(models) == 0 {
		return nil, errors.New("请输入至少一个模型 ID")
	}
	if err := switcher.ValidateModels(models, models[0].ID); err != nil {
		return nil, err
	}
	return models, nil
}
func (o uiOptions) engine(create bool) (switcher.Engine, error) {
	var e switcher.Engine
	if runtime.GOOS != "darwin" {
		return e, errors.New("第一版支持 macOS")
	}
	home, err := filepath.Abs(o.Home)
	if err != nil {
		return e, err
	}
	helper, err := switcher.StableHelper(os.Args[0])
	if err != nil {
		return e, err
	}
	store, err := switcher.OpenStore(o.Store, create)
	if err != nil {
		return e, err
	}
	return switcher.Engine{Store: store, Home: home, Helper: helper, Runtime: switcher.MacRuntime{App: o.App, Codex: o.Codex, Home: home}, OnMessage: func(s string) { fmt.Fprintln(os.Stderr, s) }}, nil
}

type menuEntry struct {
	Label  string
	Action string
}

func mainMenu(pending bool) []menuEntry {
	entries := []menuEntry{{"账户列表", "list"}, {"切换账户", "use"}, {"添加账户", "add"}, {"模型管理", "models"}, {"当前状态", "status"}, {"会话接续", "sessions"}, {"切换日志", "logs"}, {"删除账户", "remove"}}
	if pending {
		entries = append(entries, menuEntry{"恢复配置", "recover"})
	}
	return append(entries, menuEntry{"退出", "exit"})
}
func interactive(o uiOptions) error {
	notice := ""
	o.Notice = func(message string) { notice = message }
	for {
		current := "尚未添加账户"
		pending := false
		if _, err := os.Stat(filepath.Join(o.Store, "vault.enc")); err == nil {
			current = "无法读取账户"
			e, er := o.engine(false)
			if er == nil {
				pending = e.Store.HasJournal()
				st, er := e.Store.Load()
				if er == nil {
					current = switcher.ProfileLabel(st.Current, st.Profiles[st.Current])
					if current == "" {
						current = "尚未切换账户"
					}
				} else {
					notice = er.Error()
				}
			} else {
				notice = er.Error()
			}
		}
		title := "codex-switch  当前账户：" + current
		if notice != "" {
			title += "\n  " + notice
		}
		if pending {
			title += "\n  发现未完成切换，请选择恢复配置"
		}
		entries := mainMenu(pending)
		labels := []string{}
		for _, entry := range entries {
			labels = append(labels, entry.Label)
		}
		action, err := chooseMenu(menuModel{Title: title, Options: labels, Height: 24, Width: 80, Brand: true})
		if errors.Is(err, errExit) || errors.Is(err, errBack) {
			return nil
		}
		if err != nil {
			return err
		}
		if entries[action].Action == "exit" {
			return nil
		}
		notice = ""
		err = uiAction(o, entries[action].Action)
		if errors.Is(err, errExit) {
			return nil
		}
		if errors.Is(err, errBack) {
			continue
		}
		if err != nil {
			message := "操作未完成\n" + err.Error()
			failed := true
			if switcher.IsCommitted(err) {
				message = "操作已完成 · 提示\n" + err.Error()
				failed = false
			}
			_, showErr := chooseMenu(menuModel{Title: message, Options: []string{"返回"}, Height: 24, ErrorTitle: failed})
			if errors.Is(showErr, errExit) {
				return nil
			}
			if showErr != nil && !errors.Is(showErr, errBack) {
				return showErr
			}
			continue
		} else if notice == "" {
			notice = "操作已完成"
		}
	}
}

// A safe default is part of the operation, not a second confirmation screen.
func accountMenu(st switcher.State, providersOnly, removing bool) (menuModel, []string) {
	names := []string{}
	for n, p := range st.Profiles {
		if (!providersOnly || p.Kind == "provider") && (!removing || n != st.Current) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	m := menuModel{Title: "选择账户", Cursor: len(names), Height: 24}
	for _, n := range names {
		p := st.Profiles[n]
		kind := "官方 GPT"
		if p.Kind == "provider" {
			kind = "第三方"
		}
		label := switcher.ProfileDisplay(n, p) + " · " + kind
		if n == st.Current {
			label += " · 当前"
			if !removing {
				m.Cursor = len(m.Options)
			}
		}
		m.Options = append(m.Options, label)
	}
	m.Options = append(m.Options, "返回")
	return m, names
}
func account(e switcher.Engine, providersOnly bool, removing ...bool) (string, switcher.Profile, error) {
	st, err := e.Store.Load()
	if err != nil {
		return "", switcher.Profile{}, err
	}
	m, names := accountMenu(st, providersOnly, len(removing) > 0 && removing[0])
	i, err := chooseMenu(m)
	if err != nil {
		return "", switcher.Profile{}, err
	}
	if i == len(names) {
		return "", switcher.Profile{}, errBack
	}
	if !providersOnly && !(len(removing) > 0 && removing[0]) && names[i] == st.Current {
		return "", switcher.Profile{}, errBack
	}
	return names[i], st.Profiles[names[i]], nil
}
func useSelectedAccount(e switcher.Engine, name string) error {
	st, err := e.Store.Load()
	if err != nil {
		return err
	}
	if name == st.Current {
		return errBack
	}
	use := func() error {
		return interruptedOperation(e, func(engine switcher.Engine) error { return engine.Use(name) })
	}
	force := func() error {
		return interruptedOperation(e, func(engine switcher.Engine) error { return engine.UseForced(name) })
	}
	return runAccountSwitch(name, use, chooseMenu, force)
}
func uiAction(o uiOptions, action string) error {
	if action == "add" {
		return uiAdd(o)
	}
	e, err := o.engine(false)
	if err != nil {
		return err
	}
	switch action {
	case "sessions":
		return uiSessions(e)
	case "logs":
		return uiSwitchLogs(e.Store.Dir)
	case "list":
		return uiList(e)
	case "use":
		name, _, err := account(e, false)
		if err != nil {
			return err
		}
		return useSelectedAccount(e, name)
	case "models":
		name, p, err := account(e, true)
		if err != nil {
			return err
		}
		return uiModels(e, name, *p.Provider)
	case "status":
		st, err := e.Store.Load()
		if err != nil {
			return err
		}
		return uiUsageDetail(e, st.Current, true)
	case "remove":
		name, _, err := account(e, false, true)
		if err != nil {
			return err
		}
		return e.Remove(name)
	case "recover":
		return e.Recover()
	}
	return nil
}
func addAccountTypes() []string { return []string{"官方登录", "第三方配置"} }
func uiAdd(o uiOptions) error {
	kind, err := choose("添加类型", addAccountTypes())
	if err != nil {
		return err
	}
	if kind == 1 {
		return addProviderUI(o)
	}
	name, err := input("账户备注（支持中文，用于区分账户，不是登录名）", "", false, switcher.ValidateRemark)
	if err != nil {
		return err
	}
	device := false
	if kind == 0 {
		i, err := choose("登录方式", []string{"浏览器登录", "设备登录"})
		if err != nil {
			return err
		}
		device = i == 1
	}
	e, err := o.engine(true)
	if err != nil {
		return err
	}
	return interruptedOperation(e, func(engine switcher.Engine) error { return engine.AddChatGPT(name, false, device) })
}
func uiModels(e switcher.Engine, name string, p switcher.Provider) error {
	if p.CatalogURL != "" && p.CatalogMode != "remote" {
		i, err := choose("模型目录方式", []string{"编辑列表", "远程发现"})
		if err != nil {
			return err
		}
		if i == 1 {
			return e.UseRemoteCatalog(name)
		}
	}
	if p.CatalogMode == "remote" {
		i, err := choose("该账户使用远程模型发现。手动保存列表将切换到本地模型目录。", []string{"保留远程", "编辑列表"})
		if err != nil {
			return err
		}
		if i == 0 {
			return errBack
		}
	}
	models := switcher.Models(p)
	def := p.Model
	for {
		labels := []string{}
		for i, m := range models {
			if i == 6 {
				labels = append(labels, fmt.Sprintf("… 共 %d 个模型，可在移除/默认菜单查看全部", len(models)))
				break
			}
			s := m.ID
			if s == def {
				s += "（默认）"
			}
			labels = append(labels, s)
		}
		title := "模型列表 · " + name + "\n  " + strings.Join(labels, "\n  ")
		action, err := choose(title, []string{"新增模型", "移除模型", "默认模型", "保存", "返回"})
		if err != nil {
			return err
		}
		switch action {
		case 0:
			id, er := input("新增模型 ID", "", false, required)
			if errors.Is(er, errBack) {
				continue
			}
			if er != nil {
				return er
			}
			next := append(append([]switcher.Model(nil), models...), switcher.Model{ID: id})
			if er = switcher.ValidateModels(next, def); er != nil {
				_, ce := choose(er.Error(), []string{"返回"})
				if errors.Is(ce, errExit) {
					return ce
				}
				continue
			}
			models = next
		case 1:
			if len(models) == 1 {
				_, er := choose("至少需要保留一个模型", []string{"返回"})
				if errors.Is(er, errExit) {
					return er
				}
				continue
			}
			i, er := chooseMenu(menuModel{Title: "选择要移除的模型", Options: append(modelLabels(models, def), "返回"), Cursor: len(models), Height: 24})
			if errors.Is(er, errBack) {
				continue
			}
			if er != nil {
				return er
			}
			if i == len(models) {
				continue
			}
			removed := models[i].ID
			models = append(models[:i], models[i+1:]...)
			if removed == def {
				def = models[0].ID
			}
		case 2:
			i, er := chooseMenu(defaultModelMenu(models, def))
			if errors.Is(er, errBack) {
				continue
			}
			if er != nil {
				return er
			}
			def = models[i].ID
		case 3:
			if reflect.DeepEqual(models, switcher.Models(p)) && def == p.Model && p.CatalogMode != "remote" {
				return errBack
			}
			return e.UpdateModels(name, models, def)
		case 4:
			return errBack
		}
	}
}

func profileDescription(p switcher.Profile) string {
	if p.Kind == "provider" && p.Provider != nil {
		mode := "本地列表"
		if p.Provider.CatalogMode == "remote" {
			mode = "远程发现"
		}
		return "第三方 · " + p.Provider.Model + " · " + mode
	}
	model, _ := p.Route["model"].(string)
	if model == "" {
		model = "客户端默认"
	}
	return "官方账户 · " + model
}
func uiList(e switcher.Engine) error { return uiAccounts(e) }

func modelLabels(models []switcher.Model, def string) []string {
	labels := make([]string, 0, len(models))
	for _, m := range models {
		label := m.ID
		if m.ID == def {
			label += "（默认）"
		}
		labels = append(labels, label)
	}
	return labels
}
func defaultModelMenu(models []switcher.Model, def string) menuModel {
	m := menuModel{Title: "选择默认模型", Options: append(modelLabels(models, def), "返回"), Height: 24}
	for i, model := range models {
		if model.ID == def {
			m.Cursor = i
			break
		}
	}
	return m
}
