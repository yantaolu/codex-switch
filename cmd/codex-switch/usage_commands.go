package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"codex-switch/internal/switcher"
	"golang.org/x/term"
)

func statusCommand(e switcher.Engine, args []string) error {
	name := ""
	force := false
	for _, arg := range args {
		if arg == "--refresh" {
			force = true
		} else if name == "" && switcher.ValidateName(arg) == nil {
			name = arg
		} else {
			return errors.New("用法：status [名称] [--refresh]")
		}
	}
	st, err := e.Store.Load()
	if err != nil {
		return err
	}
	if name == "" {
		name = st.Current
	}
	p, ok := st.Profiles[name]
	if !ok {
		return errors.New("账户不存在或尚未选择当前账户")
	}
	fmt.Println(titleStyle.Render("账户状态 · " + name))
	fmt.Println(profileDescription(p))
	fmt.Println("认证：", switcher.CredentialStatus(p))
	if name == st.Current {
		diagnostic, err := e.Inspect()
		if err != nil {
			return err
		}
		fmt.Println("实际认证与记录一致：", diagnostic.AuthMatches)
		fmt.Println("待恢复切换：", diagnostic.Pending)
	}
	u, err := e.QueryUsage(context.Background(), name, force)
	if err != nil {
		return err
	}
	fmt.Println(safeUI(usageLines(u)))
	if u.Error != "" {
		return errors.New("额度查询未完成，原因见上方")
	}
	return nil
}
func quotaCommand(e switcher.Engine, args []string) error {
	if len(args) != 1 && !(len(args) == 3 && args[1] == "--file") {
		return errors.New("用法：quota <名称> [--file JSON]")
	}
	if len(args) == 3 {
		b, err := switcher.ReadImportFile(args[2])
		if err != nil {
			return fmt.Errorf("无法读取查询配置：%w", err)
		}
		c, err := decodeUsage(b)
		if err != nil {
			return err
		}
		return e.UpdateUsage(args[0], c)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("交互配置需要终端；也可使用 --file JSON")
	}
	st, err := e.Store.Load()
	if err != nil {
		return err
	}
	p, ok := st.Profiles[args[0]]
	if !ok || p.Kind != "provider" {
		return errors.New("请选择第三方账户")
	}
	err = uiUsageConfig(e, args[0], p)
	if errors.Is(err, errBack) || errors.Is(err, errExit) {
		return nil
	}
	return err
}
