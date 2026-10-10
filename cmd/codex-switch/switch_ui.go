package main

import (
	"codex-switch/internal/switcher"
	"errors"
)

func parseUse(args []string) (string, bool, error) {
	name := ""
	force := false
	for _, arg := range args {
		if arg == "--force" && !force {
			force = true
		} else if name == "" && switcher.ValidateName(arg) == nil {
			name = arg
		} else {
			return "", false, errors.New("用法：use <备注> [--force]")
		}
	}
	if name == "" {
		return "", false, errors.New("用法：use <备注> [--force]")
	}
	return name, force, nil
}

func switchFailure(name string, err error) menuModel {
	return menuModel{
		Title:   "切换失败 · " + name + "\n" + err.Error(),
		Options: []string{"重试切换", "返回"},
		Cursor:  1, Height: 24, ErrorTitle: true,
	}
}

// Failures stay in the account-switch flow; returning never creates a main-menu notice.
func runAccountSwitch(name string, use func() error, showFailure func(menuModel) (int, error), forced ...func() error) error {
	forceMode := false
	for {
		execute := use
		if forceMode {
			execute = forced[0]
		}
		if err := execute(); err != nil {
			if switcher.IsCommitted(err) {
				_, showErr := showFailure(menuModel{Title: "已切换 · " + name + "\n" + err.Error(), Options: []string{"返回"}, Height: 24})
				if showErr != nil {
					return showErr
				}
				return nil
			}
			menu := switchFailure(name, err)
			if switcher.NeedsRecovery(err) || switcher.IsUnconfirmed(err) {
				menu.Options = []string{"返回"}
				menu.Cursor = 0
				_, showErr := showFailure(menu)
				if showErr != nil {
					return showErr
				}
				return errBack
			}
			var conflict *switcher.ProcessConflictError
			allowForce := len(forced) > 0 && errors.As(err, &conflict)
			if allowForce {
				menu.Title += "\n强制切换会结束 Codex 关联任务。"
				menu.Options = []string{"重试切换", "强制切换", "返回"}
				menu.Cursor = 2
			}
			i, showErr := showFailure(menu)
			if showErr != nil {
				return showErr
			}
			if i != 0 && !(allowForce && i == 1) {
				return errBack
			}
			forceMode = allowForce && i == 1
			continue
		}
		return nil
	}
}
