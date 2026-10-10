package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

type helpRow struct{ Command, Description string }

var helpRows = []helpRow{
	{"ui", "打开交互菜单"},
	{"init", "初始化加密存储"},
	{"add chatgpt <备注> --current", "保存当前官方登录"},
	{"add chatgpt <备注>", "登录新的官方账户"},
	{"add provider <备注>", "粘贴第三方配置"},
	{"add provider <备注> --toml-file <文件>", "从 TOML 文件导入"},
	{"list", "列出账户 ID、备注与类型"},
	{"remark <ID> <备注>", "修改显示备注，支持中文"},
	{"edit <ID>", "编辑指定第三方配置"},
	{"login <ID>", "重新认证原官方账户"},
	{"backup <文件>", "导出可移植加密备份"},
	{"restore <文件>", "恢复到空账户库，不自动激活"},
	{"doctor", "检查存档和原生 CLI"},
	{"status [名称] [--refresh]", "查看状态、订阅和额度"},
	{"use <备注> [--force]", "退出进程并切换账户"},
	{"remove <备注>", "删除非当前账户"},
	{"models <备注>", "交互配置账户模型"},
	{"models <备注> <ID1,ID2> <默认ID>", "修改账户模型列表"},
	{"quota <备注> [--file <JSON>]", "配置账户额度查询"},
	{"recover", "恢复中断的切换"},
	{"sessions [会话ID]", "列出会话或用当前账户创建接续副本"},
	{"logs", "查看最近切换记录"},
	{"version", "显示版本"},
}

func helpText(width int) string {
	if width < 24 {
		width = 24
	}
	col := 0
	for _, row := range helpRows {
		if n := lipgloss.Width(row.Command); n > col {
			col = n
		}
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("codex-switch") + " · 账户与服务商切换\n\n")
	b.WriteString(ansi.Wrap("直接运行进入交互菜单；↑↓ 循环选择，Enter 确认，Esc 返回。", width, " ") + "\n\n")
	b.WriteString(titleStyle.Render("用法") + "  codex-switch [全局选项] <命令>\n\n")
	for _, row := range helpRows {
		command := currentStyle.Render(row.Command)
		if col+4+16 <= width {
			prefix := "  " + command + strings.Repeat(" ", col-lipgloss.Width(row.Command)+2)
			lines := strings.Split(ansi.Wrap(row.Description, width-col-4, " "), "\n")
			b.WriteString(prefix + mutedStyle.Render(lines[0]) + "\n")
			for _, line := range lines[1:] {
				b.WriteString(strings.Repeat(" ", col+4) + mutedStyle.Render(line) + "\n")
			}
		} else {
			b.WriteString(ansi.Wrap("  "+command, width, " ") + "\n")
			b.WriteString("    " + mutedStyle.Render(ansi.Wrap(row.Description, width-4, " ")) + "\n")
		}
	}
	for _, line := range []string{"", "全局选项置于命令前：--home、--store、--app、--codex", "命令后加 --help 查看用法；帮助不读取账户数据。", "添加接受中文备注；list 显示稳定 ID，其余账户命令使用 ID。", "首次使用：add chatgpt <备注> --current"} {
		b.WriteString(ansi.Wrap(line, width, " ") + "\n")
	}
	return b.String()
}
func help() {
	width := 100
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		width = w
	}
	fmt.Print(helpText(width))
}
