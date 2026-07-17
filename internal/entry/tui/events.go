package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/voocel/ainovel-cli/internal/diag"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/entry/startup"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/store"
)

// Các kiểu message
type (
	eventMsg       host.Event
	snapshotMsg    host.UISnapshot
	doneMsg        struct{ complete bool } // complete=true toàn bộ sách hoàn thành, false dừng do lỗi
	abortResultMsg struct{ stopped bool }
	bootstrapMsg   struct {
		replay    []domain.RuntimeQueueItem
		resumed   bool
		completed bool // 目录里是本已完结的书：落完成态工作台而非欢迎页
		err       error
	}
	reportLoadedMsg struct {
		reqID      int
		report     diag.Report
		exportPath string // đường dẫn tuyệt đối của file chẩn đoán đã ẩn danh; rỗng = xuất thất bại
		finishedAt time.Time
	}
	askUserMsg       askUserRequest
	startResultMsg   struct{ err error }
	cocreateDeltaMsg struct {
		reqID int
		kind  string // host.CoCreateProgressThinking | host.CoCreateProgressReply
		text  string
	}
	// cocreateStreamItem là payload nội bộ của deltaCh, gửi cả kind streaming và văn bản tích lũy đến TUI.
	cocreateStreamItem struct {
		kind string
		text string
	}
	cocreateDoneMsg struct {
		reqID int
		reply host.CoCreateReply
		err   error
	}
	steerResultMsg     struct{}
	continueResultMsg  struct{ err error }
	spinnerTickMsg     time.Time
	toolSpinnerTickMsg time.Time // spinner độc lập cho luồng sự kiện công cụ (nhanh hơn, độc lập với thanh đầu/ngôi sao)
	cursorTickMsg      time.Time // tick độc lập cho con trỏ streaming
	streamDeltaMsg     string    // token delta của streaming
	streamClearMsg     struct{}  // xóa buffer streaming (bắt đầu tin nhắn mới)
	streamFlushTickMsg struct{}  // làm tươi panel streaming 60fps có giới hạn (gộp delta cấp token)
	quitResetMsg       struct{}  // reset timeout sau hai lần nhấn Ctrl+C
)

// --- Các hàm Cmd ---

func listenEvents(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-rt.Events()
		if !ok {
			return nil
		}
		return eventMsg(ev)
	}
}

func listenDone(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		_, ok := <-rt.Done()
		if !ok {
			return nil
		}
		snap := rt.Snapshot()
		return doneMsg{complete: snap.Phase == "complete"}
	}
}

func tickSnapshot(rt *host.Host) tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return snapshotMsg(rt.Snapshot())
	})
}

func fetchSnapshot(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		return snapshotMsg(rt.Snapshot())
	}
}

func bootstrapRuntime(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		replay, err := rt.ReplayQueue(0)
		if err != nil {
			return bootstrapMsg{err: err}
		}
		label, err := rt.Resume()
		if err != nil {
			return bootstrapMsg{replay: replay, err: err}
		}
		if label == "" {
			// 完结书不可续跑（恢复视为无标签），但也不能落欢迎页装作书不存在——
			// 直接落到完成态工作台：面板照常展示本书，/reopen、/export、返工输入都在原位。
			if rt.Snapshot().Phase == "complete" {
				return bootstrapMsg{replay: replay, completed: true}
			}
			if len(replay) == 0 {
				return nil
			}
		}
		return bootstrapMsg{replay: replay, resumed: label != ""}
	}
}

// resumeBook 会话中补跑一次恢复门禁（bootstrap 的 Resume 只在启动时跑一次）：
// 导入完成关面板、/reopen 重开后都靠它落回创作工作台。不重放事件队列——本会话事件
// 已由常驻 listenEvents 呈现过，重放会重复回显。待处理干预（如 /reopen 登记的续写
// 方向）由 Resume 先经 Arbiter 裁定消化，再续跑引擎。
func resumeBook(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		label, err := rt.Resume()
		return bootstrapMsg{resumed: label != "", err: err}
	}
}

func startRuntime(rt *host.Host, plan startup.Plan) tea.Cmd {
	return func() tea.Msg {
		// 启动侧确定性生成本书用户规则快照（用原始 prompt 归一化），须在 StartPrepared 前。
		if err := rt.PrepareUserRules(plan.RawPrompt); err != nil {
			return startResultMsg{err: err}
		}
		err := rt.StartPrepared(plan.RawPrompt)
		return startResultMsg{err: err}
	}
}

func runCoCreate(rt *host.Host, state *cocreateState) tea.Cmd {
	history := state.session.History()
	ctx, cancel := context.WithCancel(context.Background())
	state.cancel = cancel
	state.deltaCh = make(chan cocreateStreamItem, 64)
	state.doneCh = make(chan cocreateDoneMsg, 1)
	// Đồng sáng tác theo giai đoạn mang tóm tắt trạng thái câu chuyện, tạo ra "brief hướng đi tiếp theo";
	// khởi động lạnh làm rõ yêu cầu từ đầu. Cả hai có chữ ký giống nhau.
	stream := rt.CoCreateStream
	if state.stage {
		stream = rt.StageCoCreateStream
	}
	start := func() tea.Msg {
		go func() {
			reply, err := stream(ctx, history, func(kind, text string) {
				select {
				case state.deltaCh <- cocreateStreamItem{kind: kind, text: text}:
				default:
				}
			})
			state.doneCh <- cocreateDoneMsg{reply: reply, err: err}
			close(state.deltaCh)
			close(state.doneCh)
		}()
		return nil
	}
	return tea.Batch(start, listenCoCreateDelta(state), listenCoCreateDone(state))
}

func listenCoCreateDelta(state *cocreateState) tea.Cmd {
	if state == nil || state.deltaCh == nil {
		return nil
	}
	// Lấy tham chiếu cục bộ của channel: tránh trường hợp state.deltaCh bị reassign sau này
	// khiến closure listen cũ đọc nhầm channel mới (dù luồng hiện tại không kích hoạt,
	// để như vậy là bẫy bảo trì không nên để lại).
	reqID := state.reqID
	ch := state.deltaCh
	return func() tea.Msg {
		item, ok := <-ch
		if !ok {
			return nil
		}
		return cocreateDeltaMsg{reqID: reqID, kind: item.kind, text: item.text}
	}
}

func listenCoCreateDone(state *cocreateState) tea.Cmd {
	if state == nil || state.doneCh == nil {
		return nil
	}
	reqID := state.reqID
	ch := state.doneCh
	return func() tea.Msg {
		result, ok := <-ch
		if !ok {
			return nil
		}
		result.reqID = reqID
		return result
	}
}

func steerRuntime(rt *host.Host, text string) tea.Cmd {
	return func() tea.Msg {
		rt.Steer(text)
		return steerResultMsg{}
	}
}

func continueRuntime(rt *host.Host, text string) tea.Cmd {
	return func() tea.Msg {
		err := rt.Continue(text)
		return continueResultMsg{err: err}
	}
}

// resumeFromCoCreate đưa brief hướng đi tiếp theo từ đồng sáng tác theo giai đoạn vào và tiếp tục sáng tác.
// Tái sử dụng continueResultMsg: thành công thì nối listenDone để tiếp tục chạy, thất bại thì hiển thị lỗi.
func resumeFromCoCreate(rt *host.Host, draft string) tea.Cmd {
	return func() tea.Msg {
		err := rt.ResumeFromCoCreate(draft)
		return continueResultMsg{err: err}
	}
}

// cancelCoCreate hủy đồng sáng tác theo giai đoạn: xóa cờ đang chiếm dụng, giữ trạng thái tạm dừng.
// Sự kiện được hồi lưu qua kênh events, không cần trả về message.
func cancelCoCreate(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		rt.CancelCoCreate()
		return nil
	}
}

func abortRuntime(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		return abortResultMsg{stopped: rt.Abort()}
	}
}

func loadReport(dir string, reqID int) tea.Cmd {
	return func() tea.Msg {
		s := store.NewStore(dir)
		// Diagnose = chẩn đoán sáng tác + phát hiện runtime; Finding runtime cũng được đưa lên báo cáo màn hình.
		rep, rc := diag.Diagnose(s)
		// Tái sử dụng rep+rc để ghi file chẩn đoán đã ẩn danh (xuất thất bại không ảnh hưởng báo cáo trên màn hình).
		exportPath, _ := diag.WriteExport(s, rep, rc)
		return reportLoadedMsg{
			reqID:      reqID,
			report:     rep,
			exportPath: exportPath,
			finishedAt: time.Now(),
		}
	}
}

func tickSpinner() tea.Cmd {
	return tea.Tick(350*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}

// tickToolSpinner điều khiển spinner của dòng "đang xử lý" trong luồng sự kiện.
// Độc lập với tickSpinner, nhịp nhanh hơn (150ms).
func tickToolSpinner() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return toolSpinnerTickMsg(t)
	})
}

func tickCursor() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return cursorTickMsg(t)
	})
}

// tickStreamFlush điều khiển làm tươi có giới hạn cho panel streaming. streamDelta không còn
// re-render ngay mỗi token mà đánh dấu dirty; tick này kiểm tra và gộp làm tươi mỗi 16ms (~60fps),
// giảm "hàng chục lần re-render toàn phần mỗi giây" trong giai đoạn streaming tốc độ cao của LLM
// xuống giới hạn 60 lần.
func tickStreamFlush() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg {
		return streamFlushTickMsg{}
	})
}

func listenStream(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		delta, ok := <-rt.Stream()
		if !ok {
			return nil
		}
		// sentinel được dispatch thành streamClearMsg, đảm bảo đến TUI theo đúng thứ tự emit
		// trong cùng một kênh với delta bình thường. Khi dùng hai kênh, clearCh và streamCh
		// không có thứ tự, khiến header ✻ thường bị nhét nhầm vào cuối đoạn thinking trước.
		if delta == host.StreamClearSentinel {
			return streamClearMsg{}
		}
		return streamDeltaMsg(delta)
	}
}

func listenAskUser(bridge *askUserBridge) tea.Cmd {
	return func() tea.Msg {
		req, ok := <-bridge.requests
		if !ok {
			return nil
		}
		return askUserMsg(req)
	}
}
