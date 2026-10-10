package main

import (
	"codex-switch/internal/switcher"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

func backupPassword(stdin bool, confirm bool) (string, error) {
	if stdin {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 4098))
		if err != nil {
			return "", err
		}
		password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		if len(password) < 12 || len(password) > 4096 {
			return "", errors.New("备份密码须为 12–4096 字节")
		}
		return password, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("自动化备份请使用 --password-stdin，密码不接受命令行参数")
	}
	fmt.Fprint(os.Stderr, "备份密码：")
	data, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if confirm {
		fmt.Fprint(os.Stderr, "再次输入：")
		other, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		if string(data) != string(other) {
			return "", errors.New("两次密码不一致")
		}
	}
	return string(data), nil
}
func backupArgs(args []string) (string, bool, error) {
	if len(args) == 1 {
		return args[0], false, nil
	}
	if len(args) == 2 && args[1] == "--password-stdin" {
		return args[0], true, nil
	}
	return "", false, errors.New("用法：backup|restore <文件> [--password-stdin]")
}
func backupCommand(e switcher.Engine, args []string) error {
	path, stdin, err := backupArgs(args)
	if err != nil {
		return err
	}
	password, err := backupPassword(stdin, true)
	if err != nil {
		return err
	}
	if err = e.ExportBackup(path, password); err != nil {
		return err
	}
	fmt.Println("已保存加密备份：", path)
	return nil
}
func restoreBackupCommand(e switcher.Engine, args []string) error {
	path, stdin, err := backupArgs(args)
	if err != nil {
		return err
	}
	password, err := backupPassword(stdin, false)
	if err != nil {
		return err
	}
	if err = e.ImportBackup(path, password); err != nil {
		return err
	}
	fmt.Println("已恢复账户库；尚未切换账户，请运行 list 后选择账户。")
	return nil
}
func doctorCommand(e switcher.Engine, args []string) error {
	if len(args) != 0 {
		return errors.New("用法：doctor")
	}
	st, err := e.Store.Load()
	if err != nil {
		return err
	}
	fmt.Printf("存档版本：%d · 账户数：%d · 待恢复：%v\n", st.Version, len(st.Profiles), e.Store.HasJournal())
	fmt.Println("认证助手：", e.Helper)
	if _, err := switcher.FindCodex(); err != nil {
		fmt.Println("原生 CLI：未找到；存档管理仍可使用")
	} else {
		fmt.Println("原生 CLI：可用")
	}
	diagnostic, err := e.Inspect()
	if err != nil {
		return err
	}
	if diagnostic.Current != "" {
		fmt.Printf("当前账户：%s · 认证一致：%v\n", diagnostic.Current, diagnostic.AuthMatches)
	}
	if diagnostic.HelperMigration {
		fmt.Printf("旧认证助手需要迁移；退出客户端后运行：codex-switch use %s\n", diagnostic.Current)
	}
	return nil
}

// Edit uses the same paste editor and keeps model lists that are managed separately.
func editProviderUI(e switcher.Engine, id string, p switcher.Provider) error {
	raw, err := switcher.ProviderTOML(p)
	if err != nil {
		return err
	}
	editor := newProviderEditor(func(ctx context.Context, _ string, next switcher.Provider) error {
		next.Models = p.Models
		next.AuthMode = p.AuthMode
		if next.CatalogURL == p.CatalogURL {
			next.CatalogMode = p.CatalogMode
		}
		if len(next.Models) > 0 {
			found := false
			for _, m := range next.Models {
				if m.ID == next.Model {
					found = true
				}
			}
			if !found {
				next.Models = append(next.Models, switcher.Model{ID: next.Model})
			}
		}
		return e.WithContext(ctx).UpdateProvider(id, next)
	})
	editor.Name.SetValue(id)
	editor.Config.SetValue(string(raw))
	editor.Edit = true
	return runEditingProvider(editor)
}
