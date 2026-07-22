package rules

import (
	"os"
	"path/filepath"
)

// LoadOptions 枚举 rules 文件来源目录，供 RawFileSources 扫描归一化。
//
// 目录不存在不算错误，扫描时静默跳过。
type LoadOptions struct {
	// HomeRulesDir 是 ~/.ainovel/rules/ 目录；扫描其下所有顶层 .md（文件名字典序合并）。空表示跳过。
	HomeRulesDir string

	// ProjectRulesDir 是 ./.ainovel/rules/ 目录（镜像全局，同样扫描其下所有顶层 .md）。空表示跳过。
	ProjectRulesDir string
}

// ainovelDirName 是 ainovel 在 user / project 两级共用的 dotdir 名。
// 全局 ~/.ainovel/rules/ 与项目 ./.ainovel/rules/ 由此对称。
const ainovelDirName = ".ainovel"

// DefaultProjectRulesDir 拼出 ./.ainovel/rules/ 的绝对路径（基于给定项目目录）。
// 调用方传入项目根，避免在 loader 内部依赖 cwd；镜像 DefaultHomeRulesDir。
func DefaultProjectRulesDir(projectDir string) string {
	if projectDir == "" {
		return ""
	}
	return filepath.Join(projectDir, ainovelDirName, "rules")
}

// DefaultHomeRulesDir 拼出 ~/.ainovel/rules/ 目录的绝对路径。
// home 解析失败返回空串（调用方据此跳过该来源）。
func DefaultHomeRulesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ainovelDirName, "rules")
}

// homeRulesReadme 是首次引导时写入 ~/.ainovel/rules/README.txt 的说明。
// 刻意用 .txt 后缀而非 .md——扫描只认 .md，这份说明不会被当成规则归一化。
const homeRulesReadme = `Đặt tùy chọn viết toàn cục tại đây; chúng có hiệu lực cho mọi truyện.

Tạo một file .md mới (ví dụ my-style.md) và viết yêu cầu bằng ngôn ngữ tự nhiên là được——
không cần định dạng đặc biệt, không cần YAML:

    # Nhân vật
    - Nhân vật chính Lâm Trần đừng viết thành thánh mẫu; chỉ cần ngoài lạnh trong nóng.
    # Phong cách
    - Dùng cảm giác cơ thể (đốt ngón tay trắng bệch) thay cho nhãn cảm xúc (căng thẳng).
    - Đối thoại đừng quá văn vẻ; mỗi chương khoảng 3000 chữ.
    - Không xuất hiện kiểu giọng AI như "某种程度上".

Viết xong không cần xử lý định dạng: hệ thống sẽ dùng model để chuẩn hóa các yêu cầu tự nhiên này
thành ràng buộc có cấu trúc (khoảng số chữ, từ cấm, ngưỡng từ gây mệt mỏi...),
tự động tuân thủ khi sáng tác và tự kiểm tra khi submit.

Nhiều file .md sẽ được hợp nhất theo thứ tự tên file; file ẩn bắt đầu bằng dấu chấm và file không phải .md sẽ bị bỏ qua
(vì vậy README.txt này sẽ không bị coi là quy tắc).

Đường cơ sở cơ học cho các sáo ngữ AI và từ gây mệt mỏi phổ biến đã được tích hợp sẵn; có thể dùng ngay cả khi bạn không viết gì.

Độ ưu tiên tải (cao → thấp): ./.ainovel/rules/*.md (truyện hiện tại) > ~/.ainovel/rules/*.md (tại đây) > mặc định tích hợp
`

// EnsureHomeRulesDir 尽力创建 ~/.ainovel/rules/ 目录并写入 README.txt 引导，
// 让用户发现这个全局偏好扩展点、知道怎么写。
// nice-to-have，非关键路径：home 解析失败或写入出错都静默吞掉，绝不阻断启动。
func EnsureHomeRulesDir() {
	if dir := DefaultHomeRulesDir(); dir != "" {
		_ = ensureRulesDirAt(dir)
	}
}

// ensureRulesDirAt 创建目录并把 README.txt 写成当前引导模板，是 EnsureHomeRulesDir 的可测内核。
// README.txt 是系统生成的引导文件（用户偏好写在 *.md，它不被扫描加载），每次都覆盖为
// 最新模板——不保留旧内容，也就不需要任何版本兼容逻辑。
func ensureRulesDirAt(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "README.txt"), []byte(homeRulesReadme), 0o644)
}

// DefaultOptions 根据当前工作目录构造常用 LoadOptions。
//
// 适合 Host 启动时调用一次，让用户规则服务复用同一份来源配置。
// 解析 cwd 失败时 ProjectRulesDir 留空（扫描会跳过该来源）。
//
// 路径语义：ProjectRulesDir 绑定 **当前工作目录（cwd）** 而非 outputDir。
// 用户 cd 到不同目录启动写不同的书，./.ainovel/rules/ 自然跟着 cwd 走；如需跨书共享，
// 放 ~/.ainovel/rules/ 全局目录即可（其下所有 .md 都会被加载）。
func DefaultOptions() LoadOptions {
	cwd, _ := os.Getwd()
	return LoadOptions{
		HomeRulesDir:    DefaultHomeRulesDir(),
		ProjectRulesDir: DefaultProjectRulesDir(cwd),
	}
}
