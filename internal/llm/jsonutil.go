package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExtractJSON 从 LLM 的自由文本中提取第一个完整的 JSON 值（对象或数组）。
// 容忍 markdown 代码块、前后解释性文字、嵌套括号与字符串内括号。
func ExtractJSON(s string) ([]byte, error) {
	start := -1
	var open, close byte
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			start = i
			open = s[i]
			if open == '{' {
				close = '}'
			} else {
				close = ']'
			}
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("文本中未找到 JSON")
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return []byte(s[start : i+1]), nil
			}
		}
	}
	// 输出被 max_tokens/网络截断：尝试补全闭合（先裁掉残缺的尾部键名，再试常见闭合后缀）
	if fixed, ok := repairTruncated(s[start:]); ok {
		return fixed, nil
	}
	return nil, fmt.Errorf("JSON 未闭合")
}

// repairTruncated 对被截断的 JSON 片段尝试启发式闭合，成功返回合法 JSON。
func repairTruncated(sub string) ([]byte, bool) {
	sub = strings.TrimRight(sub, " \t\r\n,:")
	// 结尾悬空的转义符会破坏补全，先剥掉
	sub = strings.TrimRight(sub, "\\")
	for _, suffix := range []string{"\"}", "}", "\"]", "]", "\"", ":1}", "\"}"} {
		if json.Valid([]byte(sub + suffix)) {
			return []byte(sub + suffix), true
		}
	}
	return nil, false
}

// ParseData 提取并反序列化 JSON 到 T。
func ParseData[T any](text string) (T, error) {
	var v T
	b, err := ExtractJSON(text)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("JSON 解析失败: %w", err)
	}
	return v, nil
}
