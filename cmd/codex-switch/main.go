package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"

	"codex-switch/internal/switcher"
	"golang.org/x/term"
)

var version = "1.0.0"

func main() {
	switcher.ClientVersion = version
	if err := run(os.Args[1:]); err != nil {
		if switcher.IsCommitted(err) {
			fmt.Fprintln(os.Stderr, "操作已完成，提示：", err)
			return
		}
		fmt.Fprintln(os.Stderr, "错误：", err)
		if switcher.NeedsRecovery(err) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
func run(args []string) error {
	h, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		home = filepath.Join(h, ".codex")
	}
	fs := flag.NewFlagSet("codex-switch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = help
	homeOpt := fs.String("home", home, "Codex 数据目录")
	dir := fs.String("store", filepath.Join(h, ".local/share/codex-switch"), "加密账户存储目录")
	app := fs.String("app", "", "桌面 APP 路径；默认在需要退出时发现")
	cli := fs.String("codex", "", "官方 Codex CLI 路径")
	// Accept the old flag for existing scripts; all operations now leave the APP stopped.
	fs.Bool("no-restart", false, "兼容旧脚本；切换始终不启动 APP")
	if err = fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	a := fs.Args()
	if len(a) > 0 && a[0] != "credential" {
		for _, arg := range a[1:] {
			if arg == "--help" || arg == "-h" {
				return commandHelp(a)
			}
		}
	}
	if len(a) > 0 && a[0] == "ui" {
		if len(a) != 1 {
			return errors.New("ui 不接受参数")
		}
		if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
			return errors.New("交互菜单需要终端")
		}
		return interactive(uiOptions{Home: *homeOpt, Store: *dir, App: *app, Codex: *cli})
	}
	if len(a) == 0 && term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		return interactive(uiOptions{Home: *homeOpt, Store: *dir, App: *app, Codex: *cli})
	}
	if len(a) == 0 || a[0] == "help" {
		help()
		return nil
	}
	if a[0] == "version" {
		fmt.Println(version)
		return nil
	}
	if !knownCommand(a[0]) {
		return errors.New("未知命令；运行 codex-switch --help 查看用法")
	}
	if a[0] == "logs" {
		if len(a) != 1 {
			return errors.New("用法：logs")
		}
		return printSwitchLogs(*dir)
	}
	if a[0] == "backup" || a[0] == "restore" {
		if _, _, err := backupArgs(a[1:]); err != nil {
			return err
		}
	}
	if runtime.GOOS != "darwin" {
		return errors.New("第一版支持 macOS")
	}
	engine, err := (uiOptions{Home: *homeOpt, Store: *dir, App: *app, Codex: *cli}).engine(a[0] == "add" || a[0] == "init" || a[0] == "restore")
	if err != nil {
		return err
	}
	store := engine.Store
	switch a[0] {
	case "remark":
		if len(a) != 3 {
			return errors.New("用法：remark <ID> <备注>")
		}
		return engine.SetRemark(a[1], a[2])
	case "edit":
		if len(a) != 2 {
			return errors.New("用法：edit <ID>")
		}
		st, err := store.Load()
		if err != nil {
			return err
		}
		p, ok := st.Profiles[a[1]]
		if !ok || p.Provider == nil {
			return errors.New("请选择第三方账户")
		}
		return editProviderUI(engine, a[1], *p.Provider)
	case "backup":
		return backupCommand(engine, a[1:])
	case "restore":
		return restoreBackupCommand(engine, a[1:])
	case "login":
		if len(a) != 2 {
			return errors.New("用法：login <ID>")
		}
		return interruptedOperation(engine, func(e switcher.Engine) error { return e.Relogin(a[1]) })
	case "doctor":
		return doctorCommand(engine, a[1:])
	case "sessions":
		return sessionCommand(engine, a[1:])
	case "logs":
		if len(a) != 1 {
			return errors.New("用法：logs")
		}
		return printSwitchLogs(store.Dir)
	case "init":
		unlock, e := store.Lock()
		if e != nil {
			return e
		}
		defer unlock()
		st, e := store.Load()
		if e != nil {
			return e
		}
		if e = store.Save(st); e == nil {
			fmt.Println("加密凭据库已初始化；尚未修改 Codex 配置或认证")
		}
		return e
	case "add":
		if len(a) < 3 {
			return errors.New("用法：add chatgpt|provider <备注>")
		}
		name := a[2]
		if err = switcher.ValidateRemark(name); err != nil {
			return err
		}
		if a[1] == "chatgpt" {
			f := flag.NewFlagSet("add chatgpt", flag.ContinueOnError)
			current := f.Bool("current", false, "导入当前登录")
			device := f.Bool("device-auth", false, "使用官方设备登录")
			if err = f.Parse(a[3:]); err != nil {
				return err
			}
			if len(f.Args()) > 0 {
				return errors.New("存在未知参数")
			}
			err = interruptedOperation(engine, func(e switcher.Engine) error { return e.AddChatGPT(name, *current, *device) })
		} else if a[1] == "provider" {
			f := flag.NewFlagSet("add provider", flag.ContinueOnError)
			file := f.String("file", "", "从 JSON 文件导入（包含 base_url、model、api_key、headers）")
			tomlFile := f.String("toml-file", "", "从 CC Switch/Codex TOML 文件导入")
			if err = f.Parse(a[3:]); err != nil {
				return err
			}
			if len(f.Args()) > 0 {
				return errors.New("存在未知参数")
			}
			var p switcher.Provider
			if *file != "" && *tomlFile != "" {
				return errors.New("--file 与 --toml-file 不能同时使用")
			}
			if *tomlFile != "" {
				b, e := switcher.ReadImportFile(*tomlFile)
				if e != nil {
					return fmt.Errorf("无法读取 TOML 文件：%w", e)
				}
				var warnings []string
				p, warnings, err = switcher.ImportTOML(b)
				if err != nil {
					return err
				}
				for _, w := range warnings {
					fmt.Fprintln(os.Stderr, "注意：", safeUI(w))
				}
				fmt.Fprintln(os.Stderr, "导入保留 bearer 认证；启用时 key 会写入权限 600 的 config.toml 和 auth.json")
			} else if *file != "" {
				b, e := switcher.ReadImportFile(*file)
				if e != nil {
					return fmt.Errorf("无法读取 provider 文件：%w", e)
				}
				if len(b) > 1<<20 || json.Unmarshal(b, &p) != nil {
					return errors.New("provider JSON 无效")
				}
			} else {
				if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
					return errors.New("交互添加需要终端；自动化请使用 --file 或 --toml-file")
				}
				err := runProviderEditor(name, func(ctx context.Context, name string, p switcher.Provider) error {
					return engine.WithContext(ctx).AddProvider(name, p)
				}, func(message string) { fmt.Println(message) })
				if errors.Is(err, errBack) || errors.Is(err, errExit) {
					return nil
				}
				return err
			}
			err = engine.AddProvider(name, p)
		} else {
			return errors.New("账户类型须为 chatgpt 或 provider")
		}
		if err == nil {
			fmt.Println("已添加：", name, " · ID：", switcher.ProfileID(name))
		}
		return err
	case "use":
		name, force, parseErr := parseUse(a[1:])
		if parseErr != nil {
			return parseErr
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		if force {
			err = engine.UseForcedContext(ctx, name)
		} else {
			err = engine.UseContext(ctx, name)
		}
		if err == nil {
			fmt.Println("已切换：", name, "；请手动打开 APP 或启动 CLI")
		}
		return err
	case "recover":
		if len(a) != 1 {
			return errors.New("recover 不接受参数")
		}
		err = engine.Recover()
		if err == nil {
			fmt.Println("恢复记录已处理；请重新打开桌面 APP")
		}
		return err
	case "remove":
		if len(a) != 2 {
			return errors.New("用法：remove <备注>")
		}
		return engine.Remove(a[1])
	case "models":
		if len(a) == 2 && term.IsTerminal(int(os.Stdin.Fd())) {
			st, e := store.Load()
			if e != nil {
				return e
			}
			p, ok := st.Profiles[a[1]]
			if !ok || p.Kind != "provider" || p.Provider == nil {
				return errors.New("请选择第三方账户")
			}
			return uiModels(engine, a[1], *p.Provider)
		}
		if len(a) != 4 {
			return errors.New("用法：models <provider 名称> <逗号分隔的模型 ID> <默认模型>")
		}
		models, err := parseModels(a[2])
		if err != nil {
			return err
		}
		return engine.UpdateModels(a[1], models, a[3])
	case "quota":
		return quotaCommand(engine, a[1:])
	case "status":
		return statusCommand(engine, a[1:])
	case "list":
		if len(a) != 1 {
			return errors.New(a[0] + " 不接受参数")
		}
		st, e := store.Load()
		if e != nil {
			return e
		}
		names := []string{}
		for n := range st.Profiles {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			mark := " "
			if n == st.Current {
				mark = "*"
			}
			fmt.Printf("%s %-24s %s · %s\n", mark, n, switcher.ProfileLabel(n, st.Profiles[n]), profileDescription(st.Profiles[n]))
		}
		return nil
	case "credential":
		if len(a) != 2 {
			return errors.New("内部认证命令参数无效")
		}
		key, e := engine.Credential(a[1])
		if e != nil {
			return errors.New("认证读取失败，请使用 status 检查")
		}
		fmt.Print(key)
		return nil
	default:
		return errors.New("未知命令；运行 codex-switch help 查看用法")
	}
}

func knownCommand(s string) bool {
	switch s {
	case "backup", "restore", "remark", "edit", "login", "doctor", "init", "add", "use", "recover", "remove", "models", "list", "status", "quota", "credential", "sessions", "logs":
		return true
	}
	return false
}
func commandHelp(args []string) error {
	if args[0] == "add" && len(args) > 1 && args[1] != "--help" && args[1] != "-h" {
		switch args[1] {
		case "chatgpt":
			fmt.Println("codex-switch add chatgpt <备注> [--current | --device-auth]\n--current 保存当前登录；未指定时在独立临时目录登录新的官方账户。")
		case "provider":
			fmt.Println("codex-switch add provider <备注> [--file JSON | --toml-file TOML]\n未指定文件时打开明文 TOML 编辑框，粘贴后可修改，Ctrl+S 保存。")
		default:
			return errors.New("账户类型须为 chatgpt 或 provider")
		}
		return nil
	}
	spec := map[string]string{
		"backup":  "codex-switch backup <文件> [--password-stdin]（加密备份，密码至少 12 字节）",
		"restore": "codex-switch --store <空目录> restore <备份> [--password-stdin]（仅恢复存档，不激活账户）",
		"remark":  "codex-switch remark <ID> <中文备注>（不修改稳定 ID）",
		"edit":    "codex-switch edit <ID>（交互编辑指定第三方配置）",
		"login":   "codex-switch login <ID>（重新登录原官方账户，不激活其他账户）",
		"doctor":  "codex-switch doctor（只读检查存档与原生 CLI）",
		"init":    "codex-switch init", "ui": "codex-switch ui",
		"list":     "codex-switch list（只读列出全部账户，* 表示当前账户）",
		"status":   "codex-switch status [名称] [--refresh]（查看账户状态、订阅和额度）",
		"quota":    "codex-switch quota <备注> [--file JSON]\n配置指定第三方账户的额度查询；未指定文件时打开交互配置。",
		"use":      "codex-switch use <备注> [--force]\n--force 结束 Codex 关联进程后切换；会中断运行中的任务。",
		"remove":   "codex-switch remove <备注>（删除非当前账户）",
		"recover":  "codex-switch recover（恢复崩溃或中断留下的切换记录）",
		"sessions": "codex-switch sessions [会话ID]\n不指定 ID 时列出本地会话；指定 ID 时使用当前账户创建接续副本，保留原会话，不发送模型请求。",
		"logs":     "codex-switch logs（查看最近 50 条操作记录，不含凭据）",
		"models":   "codex-switch models <备注>\ncodex-switch models <备注> <ID1,ID2> <默认ID>\n模型配置仅作用于指定的第三方账户。",
		"add":      "codex-switch add chatgpt <备注> [--current | --device-auth]\ncodex-switch add provider <备注> [--file JSON | --toml-file TOML]",
		"version":  "codex-switch version", "help": "codex-switch --help",
	}
	usage, ok := spec[args[0]]
	if !ok {
		return errors.New("未知命令")
	}
	fmt.Println(usage)
	return nil
}
