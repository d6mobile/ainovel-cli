## Tiêu chuẩn viết

Đây là các chuẩn chất lượng, không phải checklist để tick máy móc từng dòng. Chương trước hết phải tự nhiên và đứng vững, sau đó mới xét đủ tiêu chí.

- Mở đầu nhanh chóng dựng xung đột, bí ẩn, ham muốn hoặc cảm giác bất thường; hạn chế hồi tưởng trừu tượng.
- Dùng hành động, đối thoại và chi tiết giác quan để đẩy cốt truyện; hạn chế tóm lược và tổng kết.
- Đối thoại nhân vật phải khác biệt theo thân phận, có ẩn ý và mục đích hành động; không thuyết giáo.
- Thể hiện cảm xúc qua phản ứng cơ thể và lựa chọn, không dán nhãn trực tiếp.
- Biến chuyển quan hệ phải có sự kiện kích hoạt; không để từ xa lạ nhảy vọt thành tin tưởng tuyệt đối trong một chương.
- Bí mật được hé lộ theo từng đợt, không giải thích sớm các nút lớn mà dàn ý chưa yêu cầu.
- Hook cuối chương có thể là khủng hoảng, lựa chọn, dư âm cảm xúc, biến chuyển quan hệ hoặc mục tiêu chưa hoàn tất; không cần chương nào cũng làm cliffhanger phóng đại.
- **Giảm mùi AI**: khi viết phải tránh toàn bộ pattern trong `reference_pack.references.anti_ai_tone` (5 nhóm: cấu trúc/từ ngữ/miêu tả/đối thoại/nhịp). Các từ mỏi, câu khuôn và ngưỡng có thể liệt kê cơ học nằm trong `working_memory.user_rules.structured`, và được kiểm tra bắt buộc khi commit.
- **Đa dạng câu văn**: `episodic_memory.style_stats` (nếu có) là thống kê code rút ra từ phần chính văn bạn đã viết — tấm gương phản chiếu thói quen chữ nghĩa của chính bạn. Chủ động giảm các mục tần suất cao trong chương này; nguồn cố định hóa thường gặp nhất là câu đối chỉnh (“không phải… mà là…”), lượng từ thời gian đơn điệu (“mấy hơi/thừa hơi”) và chuỗi so sánh cùng kiểu. Luân phiên kiểu khép chương (câu ngắn cắt nhịp/dư âm đối thoại/tàn ảnh cảnh vật/câu hỏi bỏ ngỏ) với các chương gần đây; tránh mở đầu chương nào cũng bằng kiểu thời gian “đêm xuống/sáng sớm/tỉnh dậy”.
- **Không tóm tắt lại tiền tình**: summary, phục bút và trạng thái trong `episodic_memory` là ghi nhớ về nội dung đã viết, dùng để nối tiếp và đối chiếu, không phải vật liệu cần viết lại trong chương này. Thông tin chương trước đã trình bày chỉ được chạm lại từ góc nhìn mới khi cốt truyện cần; cấm viết lại kiểu “tóm tắt tập trước” (lặp câu xuyên chương sẽ bị `style_stats.repeated_sentences` ghi nhận).
