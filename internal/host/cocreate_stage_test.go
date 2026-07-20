package host

import (
	"context"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host/imp"
	"github.com/voocel/ainovel-cli/internal/store"
)

func newFlagTestHost(lc lifecycle, cocreating bool) *Host {
	return &Host{
		lifecycle:  lc,
		cocreating: cocreating,
		engine:     &engine{},
		events:     make(chan Event, 16),
	}
}

func TestPauseForCoCreate_NonRunningSetsFlag(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	if !h.PauseForCoCreate() {
		t.Fatal("trạng thái idle phải cho phép vào giai đoạn đồng sáng tác")
	}
	if !h.cocreating {
		t.Error("sau khi vào, cocreating phải là true")
	}
	if h.lifecycle != lifecycleIdle {
		t.Errorf("vào không ở trạng thái chạy không được đổi lifecycle, nhận %s", h.lifecycle)
	}
}

func TestPauseForCoCreate_RejectsCompleted(t *testing.T) {
	h := newFlagTestHost(lifecycleCompleted, false)
	if h.PauseForCoCreate() {
		t.Error("sau khi hoàn truyện không được cho vào giai đoạn đồng sáng tác")
	}
	if h.cocreating {
		t.Error("sau khi từ chối không được đặt cocreating")
	}
}

func TestPauseForCoCreate_RejectsReentrant(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	if h.PauseForCoCreate() {
		t.Error("đã đang đồng sáng tác thì phải từ chối vào lại")
	}
}

func TestCancelCoCreate_ClearsFlag(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	h.CancelCoCreate()
	if h.cocreating {
		t.Error("sau khi hủy, cocreating phải được xóa")
	}
	if h.lifecycle != lifecyclePaused {
		t.Errorf("hủy không được đổi lifecycle, nhận %s", h.lifecycle)
	}
}

func TestCancelCoCreate_NoopWhenNotCocreating(t *testing.T) {
	h := newFlagTestHost(lifecycleRunning, false)
	h.CancelCoCreate()
	if h.cocreating || h.lifecycle != lifecycleRunning {
		t.Error("CancelCoCreate ở trạng thái không đồng sáng tác phải là no-op")
	}
}

func TestResumeFromCoCreate_RejectsEmptyDraft(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	if err := h.ResumeFromCoCreate("   "); err == nil {
		t.Fatal("draft rỗng phải báo lỗi")
	}
	if !h.cocreating {
		t.Error("draft rỗng trả về trước khi xóa cờ, cocreating phải giữ true")
	}
}

func TestResumeFromCoCreate_RejectsWhenNotCocreating(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, false)
	err := h.ResumeFromCoCreate("## 后续走向\n- 进入第二卷")
	if err == nil || !strings.Contains(err.Error(), "không ở chế độ đồng sáng tác") {
		t.Fatalf("trạng thái không đồng sáng tác phải báo lỗi, nhận %v", err)
	}
}

func TestAcquireExclusive(t *testing.T) {
	cases := []struct {
		name       string
		lc         lifecycle
		cocreating bool
		exclusive  string
		wantErr    string
	}{
		{"running", lifecycleRunning, false, "", "đang chạy"},
		{"cocreating", lifecyclePaused, true, "", "đồng sáng tác"},
		{"busy", lifecycleIdle, false, "nhập", "đang chạy"},
		{"idle free", lifecycleIdle, false, "", ""},
		{"paused free", lifecyclePaused, false, "", ""},
	}
	drain := newFlagTestHost(lifecyclePaused, false)
	drain.engine.running = true
	if err := drain.acquireExclusive("nhập"); err == nil {
		t.Fatal("trong giai đoạn xả của engine phải từ chối tác vụ độc quyền")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newFlagTestHost(c.lc, c.cocreating)
			h.exclusive = c.exclusive
			err := h.acquireExclusive("nhập")
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("phải cho qua, nhận %v", err)
				}
				if h.exclusive != "nhập" {
					t.Fatalf("sau khi cho qua phải ghi nhận chiếm dụng, nhận %q", h.exclusive)
				}
				h.releaseExclusive()
				if h.exclusive != "" {
					t.Fatalf("sau khi giải phóng, chiếm dụng phải rỗng, nhận %q", h.exclusive)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("phải chứa %q, nhận %v", c.wantErr, err)
			}
			if !strings.Contains(err.Error(), "nhập") {
				t.Errorf("thông báo lỗi phải có action %q, nhận %v", "nhập", err)
			}
		})
	}
}

func TestExclusiveBlocksCreationEntries(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	h.exclusive = "nhập"
	if _, err := h.ImportFrom(context.Background(), imp.Options{}); err == nil {
		t.Error("trong lúc tác vụ độc quyền, ImportFrom phải bị từ chối")
	}
	if err := h.Continue("继续写"); err == nil {
		t.Error("trong lúc tác vụ độc quyền, Continue phải bị từ chối (phải chặn trước khi Arbiter ra quyết định)")
	}
	if _, err := h.Resume(); err == nil {
		t.Error("trong lúc tác vụ độc quyền, Resume phải bị từ chối")
	}
}

func TestStageCoCreate_OccupancyBlocksConcurrentEntries(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	if !h.PauseForCoCreate() {
		t.Fatal("thất bại khi vào giai đoạn đồng sáng tác")
	}

	if _, err := h.ImportFrom(context.Background(), imp.Options{}); err == nil {
		t.Error("trong cửa sổ đồng sáng tác, ImportFrom phải bị từ chối")
	}
	if err := h.StartPrepared("写个新故事"); err == nil {
		t.Error("trong cửa sổ đồng sáng tác, StartPrepared phải bị từ chối")
	}
	if _, err := h.Resume(); err == nil {
		t.Error("trong cửa sổ đồng sáng tác, Resume phải bị từ chối")
	}
	if err := h.Continue("继续写"); err == nil {
		t.Error("trong cửa sổ đồng sáng tác, Continue phải bị từ chối")
	}

	h.CancelCoCreate()
	if h.cocreating {
		t.Fatal("sau khi thoát, cờ chiếm dụng phải được gỡ")
	}
}

func TestBuildStoryStateSummary_NilStore(t *testing.T) {
	if got := buildStoryStateSummary(nil); got != "" {
		t.Errorf("nil store phải trả chuỗi rỗng, nhận %q", got)
	}
}

func TestBuildStoryStateSummary_Populated(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("影之诗", 100); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Progress.Load()
	p.CompletedChapters = []int{1, 2, 3}
	p.TotalWordCount = 12000
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "主角登临绝巅",
		OpenThreads:     []string{"师门血仇未报"},
		EstimatedScale:  "预计 4-6 卷",
	}); err != nil {
		t.Fatal(err)
	}

	got := buildStoryStateSummary(st)
	for _, want := range []string{"影之诗", "Đã hoàn thành 3 chương", "chương tiếp theo là chương 4", "主角登临绝巅", "师门血仇未报", "预计 4-6 卷"} {
		if !strings.Contains(got, want) {
			t.Errorf("tóm tắt phải chứa %q, thực tế:\n%s", want, got)
		}
	}
}
