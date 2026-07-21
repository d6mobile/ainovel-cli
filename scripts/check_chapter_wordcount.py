#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Script kiểm tra số chữ chương.
Kiểm tra file chương được chỉ định; nếu dưới 3000 chữ thì gợi ý cần mở rộng nội dung.
"""

import re
import sys
from pathlib import Path

# Sửa vấn đề mã hóa trên console Windows.
if sys.platform == 'win32':
    import io
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')


def count_chinese_words(text: str) -> int:
    """Đếm số chữ Hán trong văn bản, bỏ qua dấu câu và ký hiệu Markdown."""
    text = re.sub(r'#{1,6}\s*', '', text)
    text = re.sub(r'\*\*(.*?)\*\*', r'\1', text)
    text = re.sub(r'\*(.*?)\*', r'\1', text)
    text = re.sub(r'~~(.*?)~~', r'\1', text)
    text = re.sub(r'`(.*?)`', r'\1', text)
    text = re.sub(r'\[(.*?)\]\(.*?\)', r'\1', text)

    chinese_chars = re.findall(r'[一-鿿]', text)
    return len(chinese_chars)


def extract_content_from_chapter(file_path: Path) -> str:
    """Trích phần chính văn từ file chương, bỏ qua tiêu đề và metadata đầu file."""
    content = file_path.read_text(encoding='utf-8')
    lines = content.split('\n')

    content_start = 0
    for i, line in enumerate(lines):
        if line.startswith('#') and '章' in line:
            content_start = i + 1
            break

    return '\n'.join(lines[content_start:])


def check_chapter(file_path: str, min_words: int = 3000) -> dict:
    """Kiểm tra số chữ của một chương."""
    path = Path(file_path)
    if not path.exists():
        return {
            'file': str(path),
            'exists': False,
            'word_count': 0,
            'status': 'error',
            'message': f'File không tồn tại: {file_path}',
        }

    main_content = extract_content_from_chapter(path)
    word_count = count_chinese_words(main_content)
    status = 'pass' if word_count >= min_words else 'fail'
    message = f'Số chữ: {word_count}'
    if word_count >= min_words:
        message += ' (✓ đạt chuẩn)'
    else:
        message += f' (✗ chưa đủ, cần ít nhất {min_words} chữ)'

    return {
        'file': str(path),
        'exists': True,
        'word_count': word_count,
        'status': status,
        'message': message,
    }


def check_all_chapters(directory: str, pattern: str = '第*.md', min_words: int = 3000) -> list:
    """Kiểm tra mọi file chương khớp pattern trong một thư mục."""
    dir_path = Path(directory)
    if not dir_path.exists():
        print(f'Lỗi: thư mục không tồn tại - {directory}')
        return []

    chapter_files = sorted(dir_path.glob(pattern))
    return [check_chapter(str(chapter_file), min_words) for chapter_file in chapter_files]


def print_results(results: list, min_words: int = 3000) -> None:
    """In báo cáo kiểm tra."""
    if not results:
        print('Không tìm thấy file chương')
        return

    total_words = 0
    passed = 0
    failed = 0

    print('\n' + '=' * 60)
    print('Báo cáo kiểm tra số chữ chương')
    print('=' * 60)

    for result in results:
        if not result['exists']:
            print(f'\n❌ {result["file"]}')
            print(f'   {result["message"]}')
            continue

        total_words += result['word_count']
        if result['status'] == 'pass':
            passed += 1
            icon = '✅'
        else:
            failed += 1
            icon = '⚠️ '

        print(f'\n{icon} {Path(result["file"]).name}')
        print(f'   {result["message"]}')

    print('\n' + '-' * 60)
    print(f'Tổng cộng: {len(results)} chương | {passed} chương đạt | {failed} chương chưa đủ | Tổng số chữ: {total_words:,}')
    print('-' * 60)

    if failed > 0:
        print(f'\n⚠️  Có {failed} chương dưới {min_words} chữ; nên dùng kỹ thuật mở rộng nội dung:')
        print('   - Thêm miêu tả chi tiết về môi trường, tâm lý và hành động')
        print('   - Tăng cảnh đối thoại')
        print('   - Mở rộng hoạt động nội tâm của nhân vật')
        print('   - Bổ sung câu chuyện nền')
        print('\n   Tham khảo: references/content-expansion.md')


def main() -> None:
    """Hàm chính."""
    if len(sys.argv) < 2:
        print('Cách dùng:')
        print('  Kiểm tra một chương: python check_chapter_wordcount.py <đường-dẫn-file-chương> [số-chữ-tối-thiểu]')
        print('  Kiểm tra mọi chương: python check_chapter_wordcount.py --all <đường-dẫn-thư-mục> [số-chữ-tối-thiểu]')
        print('')
        print('Ví dụ:')
        print('  python check_chapter_wordcount.py novels/truyen/第01章.md')
        print('  python check_chapter_wordcount.py novels/truyen/第01章.md 3500')
        print('  python check_chapter_wordcount.py --all novels/truyen')
        print('  python check_chapter_wordcount.py --all novels/truyen 3500')
        return

    if sys.argv[1] == '--all':
        if len(sys.argv) < 3:
            print('Lỗi: cần chỉ định đường dẫn thư mục khi dùng --all')
            return
        directory = sys.argv[2]
        min_words = int(sys.argv[3]) if len(sys.argv) > 3 else 3000
        results = check_all_chapters(directory, min_words=min_words)
        print_results(results, min_words)
        return

    file_path = sys.argv[1]
    min_words = int(sys.argv[2]) if len(sys.argv) > 2 else 3000
    result = check_chapter(file_path, min_words)
    print_results([result], min_words)


if __name__ == '__main__':
    main()
