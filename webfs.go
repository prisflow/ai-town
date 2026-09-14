package main

import (
	"embed"
	"io/fs"
)

// webFS 内嵌前端构建产物（web/dist），最终产物是单个可执行文件。
// 构建顺序：先 cd web && npm run build，再 go build。
// 把 web/dist 目录下的所有文件都嵌入进来，加 all 代表连以 . 或 _ 开头的文件也一起嵌入
//go:embed all:web/dist
// 用来接收已经嵌入的文件系统
var webFS embed.FS

// webDist 返回 dist 子目录文件系统。
func webDist() fs.FS {
	sub, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		// 直接编译期报错
		panic(err)
	}
	return sub
}
