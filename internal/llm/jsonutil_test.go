package llm

import "testing"

func TestExtractJSONPlain(t *testing.T) {
	b, err := ExtractJSON(`{"a":1}`)
	if err != nil || string(b) != `{"a":1}` {
		t.Fatalf("plain: %s %v", b, err)
	}
}

func TestExtractJSONFenced(t *testing.T) {
	in := "```json\n{\"a\": {\"b\": 2}, \"c\":[1,2]}\n```\n后续说明"
	b, err := ExtractJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"a": {"b": 2}, "c":[1,2]}` {
		t.Fatalf("fenced: %q", b)
	}
}

func TestExtractJSONBracesInString(t *testing.T) {
	in := `前言 {"text":"注意 } 和 ] 在字符串里 {","n":3} 尾巴`
	b, err := ExtractJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"text":"注意 } 和 ] 在字符串里 {","n":3}` {
		t.Fatalf("braces: %q", b)
	}
}

func TestExtractJSONArray(t *testing.T) {
	b, err := ExtractJSON(`x [1, {"k":"}"}] y`)
	if err != nil || string(b) != `[1, {"k":"}"}]` {
		t.Fatalf("array: %s %v", b, err)
	}
}

func TestExtractJSONInvalid(t *testing.T) {
	if _, err := ExtractJSON("完全没有 JSON"); err == nil {
		t.Fatal("应当报错")
	}
}

func TestExtractJSONTruncatedRepair(t *testing.T) {
	// 截断修复：字符串写一半被切断 → 补引号闭合
	b, err := ExtractJSON(`{"intent":"farm_tend","reason":"跟着杏娘学种`)
	if err != nil || string(b) != `{"intent":"farm_tend","reason":"跟着杏娘学种"}` {
		t.Fatalf("repair string: %q %v", b, err)
	}
	// 截断修复：悬空键名/逗号被裁掉后闭合
	if _, err := ExtractJSON(`{"a":1,`); err != nil {
		t.Fatalf("repair comma: %v", err)
	}
	// 悬空键名也能补出合法 JSON
	b2, err := ExtractJSON(`{"a":`)
	if err != nil || string(b2) != `{"a":1}` {
		t.Fatalf("repair dangling key: %q %v", b2, err)
	}
	// 无法修复的残缺仍报错
	if _, err := ExtractJSON(`}{"a`); err == nil {
		t.Fatal("不可修复的残缺应当报错")
	}
}

func TestParseData(t *testing.T) {
	type out struct {
		Index  int    `json:"index"`
		Reason string `json:"reason"`
	}
	v, err := ParseData[out]("理由如下：{\"index\": 2, \"reason\": \"好\"}")
	if err != nil || v.Index != 2 || v.Reason != "好" {
		t.Fatalf("ParseData: %+v %v", v, err)
	}
}
