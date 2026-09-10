package containerlogs

import (
	"context"
	cl "controltower/internal/containerlog"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

type nginxNode struct {
	name     string
	args     []string
	children []nginxNode
}

func nginxTokens(text string) ([]string, error) {
	tokens := []string{}
	var b strings.Builder
	quoted := byte(0)
	started := false
	flush := func() {
		if started {
			tokens = append(tokens, b.String())
			b.Reset()
			started = false
		}
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '\\' && i+1 < len(text) {
			i++
			n := text[i]
			if n == 'n' {
				b.WriteByte('\n')
			} else if n == 't' {
				b.WriteByte('\t')
			} else if n == 'r' {
				b.WriteByte('\r')
			} else {
				b.WriteByte(n)
			}
			started = true
			continue
		}
		if quoted != 0 {
			if c == quoted {
				quoted = 0
			} else {
				b.WriteByte(c)
			}
			continue
		}
		if c == '\'' || c == '"' {
			quoted = c
			started = true
			continue
		}
		if c == '#' {
			flush()
			for i < len(text) && text[i] != '\n' {
				i++
			}
			continue
		}
		if strings.ContainsRune(" \t\r\n", rune(c)) {
			flush()
			continue
		}
		if c == ';' || c == '{' || c == '}' {
			flush()
			tokens = append(tokens, string(c))
			continue
		}
		b.WriteByte(c)
		started = true
	}
	if quoted != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	flush()
	return tokens, nil
}

type nginxConfig struct {
	root    *os.Root
	confDir string
	files   int
	bytes   int
	ctx     context.Context
	stack   map[string]bool
}

// Nginx commonly uses absolute sites-enabled symlinks. Resolve these inside
// the virtual host/container root, never against the reader's host namespace.
func nginxConfigPath(root *os.Root, file string) (string, error) {
	file = path.Clean("/" + strings.TrimPrefix(file, "/"))
	for hops := 0; hops < 32; hops++ {
		parts := strings.Split(strings.TrimPrefix(file, "/"), "/")
		changed := false
		for i := range parts {
			current := strings.Join(parts[:i+1], "/")
			info, e := root.Lstat(current)
			if e != nil {
				return "", e
			}
			if info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			target, e := os.Readlink(filepath.Join(root.Name(), filepath.FromSlash(current)))
			if e != nil {
				return "", e
			}
			if !path.IsAbs(target) {
				target = path.Join("/", path.Dir(current), target)
			}
			file = path.Join(target, path.Join(parts[i+1:]...))
			changed = true
			break
		}
		if !changed {
			return file, nil
		}
	}
	return "", fmt.Errorf("configuration symlink limit")
}

func (c *nginxConfig) load(file string) ([]nginxNode, error) {
	var resolveErr error
	file, resolveErr = nginxConfigPath(c.root, file)
	if resolveErr != nil {
		return nil, resolveErr
	}
	if c.ctx.Err() != nil {
		return nil, c.ctx.Err()
	}
	if c.files >= 128 || c.stack[file] {
		return nil, fmt.Errorf("configuration include limit")
	}
	c.files++
	c.stack[file] = true
	defer delete(c.stack, file)
	f, e := c.root.Open(strings.TrimPrefix(file, "/"))
	if e != nil {
		return nil, e
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	c.bytes += len(data)
	if e != nil || len(data) > 1024*1024 || c.bytes > 4*1024*1024 {
		return nil, fmt.Errorf("configuration size limit")
	}
	tokens, e := nginxTokens(string(data))
	if e != nil {
		return nil, e
	}
	at := 0
	depth := 0
	var parse func(bool) ([]nginxNode, error)
	parse = func(nested bool) ([]nginxNode, error) {
		depth++
		defer func() { depth-- }()
		if depth > 128 {
			return nil, fmt.Errorf("configuration nesting limit")
		}
		nodes := []nginxNode{}
		for at < len(tokens) {
			if tokens[at] == "}" {
				at++
				if !nested {
					return nil, fmt.Errorf("unexpected block end")
				}
				return nodes, nil
			}
			node := nginxNode{name: tokens[at]}
			at++
			for at < len(tokens) && tokens[at] != ";" && tokens[at] != "{" && tokens[at] != "}" {
				node.args = append(node.args, tokens[at])
				at++
			}
			if at == len(tokens) || tokens[at] == "}" {
				return nil, fmt.Errorf("invalid directive")
			}
			block := tokens[at] == "{"
			at++
			if block {
				node.children, e = parse(true)
				if e != nil {
					return nil, e
				}
			}
			if node.name == "include" && len(node.args) == 1 {
				pattern := node.args[0]
				if !path.IsAbs(pattern) {
					pattern = path.Join(c.confDir, pattern)
				}
				matches, err := fs.Glob(c.root.FS(), strings.TrimPrefix(pattern, "/"))
				if err != nil {
					return nil, err
				}
				if len(matches) == 0 && !strings.ContainsAny(pattern, "*?[") {
					return nil, fmt.Errorf("missing include")
				}
				for _, m := range matches {
					children, err := c.load("/" + m)
					if err != nil {
						return nil, err
					}
					nodes = append(nodes, children...)
				}
			} else {
				nodes = append(nodes, node)
			}
		}
		if nested {
			return nil, fmt.Errorf("unclosed block")
		}
		return nodes, nil
	}
	return parse(false)
}

// Configuration is read as data. Discovery never tests/reloads Nginx or runs a shell.
func discoverNginxConfig(ctx context.Context, rootPath, config, prefix, container, identity, timezone string) []cl.Source {
	root, e := os.OpenRoot(rootPath)
	if e != nil {
		return nil
	}
	defer root.Close()
	c := nginxConfig{root: root, confDir: path.Dir(config), ctx: ctx, stack: map[string]bool{}}
	nodes, e := c.load(config)
	if e != nil {
		return []cl.Source{{Container: container, Kind: "nginx_access", Reason: "Nginx 配置读取或解析失败，请检查 include 路径和读取权限"}}
	}
	formats := map[string]string{"combined": combinedFormat}
	var collect func([]nginxNode)
	collect = func(ns []nginxNode) {
		for _, n := range ns {
			if n.name == "stream" || n.name == "mail" {
				continue
			}
			if n.name == "log_format" && len(n.args) > 1 {
				args := n.args[1:]
				if strings.HasPrefix(args[0], "escape=") {
					args = args[1:]
				}
				formats[n.args[0]] = strings.Join(args, "")
			}
			collect(n.children)
		}
	}
	collect(nodes)
	sources := map[string]*cl.Source{}
	add := func(n nginxNode, domains []string) {
		if len(n.args) == 0 || n.args[0] == "off" {
			return
		}
		kind := "nginx_access"
		if n.name == "error_log" {
			kind = "nginx_error"
		}
		file := n.args[0]
		reason := ""
		if strings.Contains(file, "$") {
			reason = "动态日志路径暂不支持"
		}
		if strings.HasPrefix(file, "syslog:") || file == "stderr" || strings.HasPrefix(file, "/dev/") {
			reason = "此来源输出到标准流或 syslog，暂不支持文件查询"
		}
		if !path.IsAbs(file) {
			if prefix == "" {
				reason = "相对日志路径缺少可确认的 Nginx prefix"
			} else {
				file = path.Join(prefix, file)
			}
		}
		file = path.Clean(file)
		format := ""
		if kind == "nginx_access" {
			name := "combined"
			if len(n.args) > 1 && !strings.Contains(n.args[1], "=") {
				name = n.args[1]
			}
			format = formats[name]
			if format == "" {
				reason = "无法识别访问日志格式"
			}
		}
		key := kind + "\n" + file
		s, ok := sources[key]
		if !ok {
			s = &cl.Source{Container: container, ContainerID: identity, Kind: kind, LogDir: file, Timezone: timezone, HostDir: filepath.Join(rootPath, filepath.FromSlash(path.Dir(file))), FileName: path.Base(file), LogFormat: format, Fields: nginxFields(format, kind)}
			sources[key] = s
		} else if s.LogFormat != format {
			reason = "同一日志文件使用了多个格式，暂不支持查询"
		}
		if reason != "" {
			s.Reason = reason
		}
		if len(domains) == 0 {
			s.Shared = true
		}
		for _, d := range domains {
			if d == "_" || d == "" || strings.HasPrefix(d, "~") || strings.Contains(d, "$") {
				s.Shared = true
				continue
			}
			found := false
			for _, old := range s.Domains {
				if old == d {
					found = true
				}
			}
			if !found {
				s.Domains = append(s.Domains, strings.ToLower(d))
			}
		}
	}
	var walk func([]nginxNode, []nginxNode, []nginxNode, []string, bool)
	walk = func(ns, access, errors []nginxNode, domains []string, inServer bool) {
		localAccess, localErrors := []nginxNode{}, []nginxNode{}
		for _, n := range ns {
			switch n.name {
			case "access_log":
				localAccess = append(localAccess, n)
			case "error_log":
				localErrors = append(localErrors, n)
			case "server_name":
				domains = n.args
			}
		}
		if len(localAccess) > 0 {
			access = localAccess
			for _, n := range localAccess {
				if len(n.args) > 0 && n.args[0] == "off" {
					access = nil
					break
				}
			}
		}
		if len(localErrors) > 0 {
			errors = localErrors
		}
		if inServer {
			for _, n := range access {
				add(n, domains)
			}
			for _, n := range errors {
				add(n, domains)
			}
		}
		for _, n := range ns {
			if len(n.children) == 0 {
				continue
			}
			if n.name == "server" {
				walk(n.children, access, errors, nil, true)
			} else if n.name == "http" || inServer {
				walk(n.children, access, errors, domains, inServer)
			}
		}
	}
	walk(nodes, []nginxNode{{name: "access_log", args: []string{"logs/access.log"}}}, []nginxNode{{name: "error_log", args: []string{"logs/error.log"}}}, nil, false)
	out := []cl.Source{}
	for _, s := range sources {
		if len(s.Domains) > 100 {
			s.Domains = s.Domains[:100]
		}
		for _, domain := range s.Domains {
			if len(domain) > 253 {
				s.Domains = nil
				break
			}
		}
		s.Shared = s.Shared || len(s.Domains) > 1
		for _, d := range s.Domains {
			if strings.Contains(d, "*") {
				s.Shared = true
			}
		}
		if s.Kind == "nginx_access" && !hasField(*s, "time") && s.Reason == "" {
			s.Reason = "日志格式未包含可识别的时间字段"
		}
		info, err := root.Lstat(strings.TrimPrefix(s.LogDir, "/"))
		if err != nil || !info.Mode().IsRegular() {
			if s.Reason == "" {
				s.Reason = "日志文件不存在、不可读或输出到标准流"
			}
		}
		sum := sha256.Sum256([]byte(identity + "\n" + s.HostDir + "\n" + s.FileName + "\n" + s.LogFormat + "\n" + strings.Join(s.Domains, ",")))
		s.ID = hex.EncodeToString(sum[:])
		s.Available = s.Reason == ""
		out = append(out, *s)
	}
	return out
}

func discoverHostNginx(ctx context.Context, timezone string) []cl.Source {
	candidates := map[string]string{"/etc/nginx/nginx.conf": "", "/usr/local/nginx/conf/nginx.conf": "/usr/local/nginx", "/www/server/nginx/conf/nginx.conf": "/www/server/nginx"}
	// Master command lines identify alternate -c/-p installations without running nginx -T.
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}
		if _, e := strconv.Atoi(entry.Name()); e != nil {
			continue
		}
		b, e := os.ReadFile("/proc/" + entry.Name() + "/cmdline")
		if e != nil || !strings.HasPrefix(string(b), "nginx: master process ") {
			continue
		}
		args := strings.Fields(strings.ReplaceAll(string(b), "\x00", " "))
		config, prefix := "", ""
		for i, a := range args {
			if i+1 < len(args) {
				if a == "-c" {
					config = args[i+1]
				}
				if a == "-p" {
					prefix = args[i+1]
				}
			}
		}
		if path.IsAbs(config) {
			candidates[config] = prefix
		}
	}
	out := []cl.Source{}
	for config, prefix := range candidates {
		if _, e := os.Stat(config); e == nil {
			out = append(out, discoverNginxConfig(ctx, "/", config, prefix, "nginx-host", "host", timezone)...)
		}
	}
	return out
}
