package switcher

import (
	"bytes"
	"errors"
	"sort"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Update only account-specific feature keys, preserving the rest of the table.
func patchFeatures(b []byte, route map[string]any) ([]byte, error) {
	target, _ := route["features"].(map[string]any)
	selected := map[string]any{}
	for k, v := range target {
		if accountFeatures[k] {
			if !validOption(v, "bool") {
				return nil, errors.New("账户 feature 类型无效")
			}
			selected[k] = v
		}
	}
	replacement, err := toml.Marshal(selected)
	if err != nil {
		return nil, err
	}
	type span struct {
		start, end int
		insert     []byte
	}
	spans := []span{}
	var parser unstable.Parser
	parser.Reset(b)
	table := ""
	found := false
	for parser.NextExpression() {
		n := parser.Expression()
		keys := []string{}
		for it := n.Key(); it.Next(); {
			keys = append(keys, string(it.Node().Data))
		}
		if n.Kind == unstable.Table || n.Kind == unstable.ArrayTable {
			table = "other"
			if len(keys) == 1 && keys[0] == "features" && n.Kind == unstable.Table {
				table = "features"
				found = true
				offset := int(n.Child().Raw.Offset)
				end := bytes.IndexByte(b[offset:], '\n')
				if end < 0 {
					end = len(b)
				} else {
					end += offset + 1
				}
				insert := replacement
				if end == len(b) && len(b) > 0 && b[len(b)-1] != '\n' {
					insert = append([]byte{'\n'}, replacement...)
				}
				spans = append(spans, span{end, end, insert})
			}
			continue
		}
		if n.Kind != unstable.KeyValue {
			continue
		}
		if table == "" && len(keys) > 0 && keys[0] == "features" {
			return nil, errors.New("当前 features 使用内联或点号写法，请先改为 [features] 表以支持账户切换")
		}
		if table == "features" && len(keys) == 1 && accountFeatures[keys[0]] {
			start, end := ownedAssignmentSpan(b, int(n.Raw.Offset), int(n.Raw.Offset+n.Raw.Length))
			spans = append(spans, span{start, end, nil})
		}
	}
	if parser.Error() != nil {
		return nil, errors.New("features 配置解析失败")
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	out := []byte{}
	cursor := 0
	for _, s := range spans {
		if s.start < cursor || s.end > len(b) {
			return nil, errors.New("features 配置范围冲突")
		}
		out = append(out, b[cursor:s.start]...)
		out = append(out, s.insert...)
		cursor = s.end
	}
	out = append(out, b[cursor:]...)
	if !found && len(selected) > 0 {
		out = append(out, []byte("\n[features]\n")...)
		out = append(out, replacement...)
	}
	return out, nil
}
