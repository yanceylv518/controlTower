package containerlogs

import (
	"context"
	cl "controltower/internal/containerlog"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type dockerMount struct{ Type, Source, Destination string }
type dockerMeta struct {
	ID, Name, Image, Path, WorkingDir string
	Args                              []string
	Mounts                            []dockerMount
}
type DockerRun func(context.Context, []string) ([]byte, error)

// Docker's projection deliberately excludes Config.Env and labels.
const inspectFormat = `{"ID":{{json .Id}},"Name":{{json .Name}},"Image":{{json .Config.Image}},"Path":{{json .Path}},"Args":{{json .Args}},"WorkingDir":{{json .Config.WorkingDir}},"Mounts":{{json .Mounts}}}`

var dockerID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

func discoveryCommand(ctx context.Context, args []string) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := &cappedWriter{cancel: cancel}
	cmd := exec.CommandContext(ctx, "/usr/bin/docker", args...)
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return nil, errors.New("Docker discovery unavailable")
	}
	if out.truncated {
		return nil, errors.New("Docker discovery limit exceeded")
	}
	return out.b.Bytes(), nil
}
func Discover(ctx context.Context, run DockerRun, names []string, timezone string) cl.Inventory {
	inv := cl.Inventory{Sources: []cl.Source{}}
	data, err := run(ctx, []string{"ps", "-a", "--no-trunc", "--format", "{{.ID}}"})
	if err != nil {
		inv.Error = "无法发现容器，请检查 Docker 服务及日志读取服务权限"
		return inv
	}
	ids := strings.Fields(string(data))
	if len(ids) > 256 {
		inv.Error = "容器超过 256 个发现上限，请在本机限定查询容器"
		return inv
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			inv.Error = "容器发现超时"
			break
		}
		if !dockerID.MatchString(id) {
			continue
		}
		data, err = run(ctx, []string{"inspect", "--format", inspectFormat, id})
		if err != nil {
			inv.Error = "部分容器发现失败，稍后自动重试"
			continue
		}
		var m dockerMeta
		if json.Unmarshal(data, &m) != nil {
			inv.Error = "容器元信息格式不兼容"
			continue
		}
		source, found := sourceFromMeta(m, names, timezone)
		if !found {
			continue
		}
		if source.Available {
			root, e := os.OpenRoot(source.HostDir)
			if e != nil {
				source.Available = false
				source.Reason = "日志挂载目录不存在或不可读"
			} else {
				f, e := root.Open(".")
				if e != nil {
					source.Available = false
					source.Reason = "日志目录不可读"
				} else {
					_, e = f.Readdirnames(1)
					f.Close()
					if e != nil && !errors.Is(e, io.EOF) {
						source.Available = false
						source.Reason = "日志目录不可读"
					}
				}
				root.Close()
			}
		}
		inv.Sources = append(inv.Sources, source)
		if len(inv.Sources) >= 100 {
			inv.Error = "最多展示 100 个候选容器"
			break
		}
	}
	return inv
}
func sourceFromMeta(m dockerMeta, names []string, timezone string) (cl.Source, bool) {
	name := strings.TrimPrefix(m.Name, "/")
	if !cl.ValidName(name) {
		return cl.Source{}, false
	}
	manual := false
	if len(names) > 0 {
		for _, n := range names {
			if n == name {
				manual = true
			}
		}
		if !manual {
			return cl.Source{}, false
		}
	}
	exe := path.Base(m.Path) == "new-api" || path.Base(m.Path) == "newapi"
	// A shell entrypoint is not interpreted. Explicit executable argv is safe to identify.
	if !exe && len(m.Args) > 0 {
		exe = path.Base(m.Args[0]) == "new-api" || path.Base(m.Args[0]) == "newapi"
	}
	hint := strings.Contains(strings.ToLower(m.Image), "new-api") || strings.Contains(strings.ToLower(m.Image), "newapi") || strings.Contains(strings.ToLower(name), "new-api")
	if !manual && !exe && !hint {
		return cl.Source{}, false
	}
	s := cl.Source{Container: name, ContainerID: m.ID, Timezone: timezone}
	if !manual && !exe {
		s.Reason = "名称或镜像疑似 new-api，但未确认启动程序；可在本机指定容器作为备用"
		return s, true
	}
	logDir := "/app/logs"
	for i, a := range m.Args {
		if a == "--log-dir" || a == "-log-dir" {
			if i+1 >= len(m.Args) {
				s.Reason = "日志目录启动参数缺少值"
				return s, true
			}
			logDir = m.Args[i+1]
		}
		if strings.HasPrefix(a, "--log-dir=") {
			logDir = strings.TrimPrefix(a, "--log-dir=")
		}
		if strings.HasPrefix(a, "-log-dir=") {
			logDir = strings.TrimPrefix(a, "-log-dir=")
		}
	}
	if !path.IsAbs(logDir) {
		if !path.IsAbs(m.WorkingDir) {
			s.Reason = "相对日志目录无法解析"
			return s, true
		}
		logDir = path.Join(m.WorkingDir, logDir)
	}
	logDir = path.Clean(logDir)
	s.LogDir = logDir
	if logDir == "/" || strings.ContainsAny(logDir, "\x00\r\n") {
		s.Reason = "日志目录不安全"
		return s, true
	}
	var best *dockerMount
	for i := range m.Mounts {
		mount := &m.Mounts[i]
		dest := path.Clean(mount.Destination)
		if (logDir == dest || strings.HasPrefix(logDir, dest+"/")) && (best == nil || len(dest) > len(path.Clean(best.Destination))) {
			best = mount
		}
	}
	if best == nil || (best.Type != "volume" && best.Type != "bind") {
		s.Reason = "日志目录未挂载到宿主机，无法读取应用日志文件"
		return s, true
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(logDir, path.Clean(best.Destination)), "/")
	s.HostDir = filepath.Clean(filepath.Join(best.Source, filepath.FromSlash(rel)))
	if !filepath.IsAbs(s.HostDir) || s.HostDir == string(filepath.Separator) {
		s.Reason = "日志挂载路径不安全"
		return s, true
	}
	for _, blocked := range []string{"/proc", "/sys", "/dev"} {
		if s.HostDir == blocked || strings.HasPrefix(s.HostDir, blocked+"/") {
			s.Reason = "不支持此日志挂载路径"
			return s, true
		}
	}
	sum := sha256.Sum256([]byte(m.ID + "\n" + s.HostDir + "\n" + logDir + "\n" + timezone))
	s.ID = hex.EncodeToString(sum[:])
	s.Available = true
	return s, true
}
