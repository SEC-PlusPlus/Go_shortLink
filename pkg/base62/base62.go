// Package base62 提供 Base62 编解码功能。
// Base62 使用 0-9、A-Z、a-z 共 62 个字符，适合生成短码（不含特殊字符，URL 友好）。
package base62

const (
	// alphabet 是 Base62 的字符集，按顺序排列 0-9 A-Z a-z
	alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	base     = uint64(len(alphabet)) // 62
)

// Encode 将 uint64 类型的数字转换为 Base62 字符串。
// 数字 0 编码为字符 "0"，其余按 62 进制辗转相除。
//
// 用途：将 Redis INCR 自增计数器值编码为短码。
// 示例：12345 → "3D7"
func Encode(id uint64) string {
	if id == 0 {
		return string(alphabet[0]) // "0"
	}

	var buf []byte
	for id > 0 {
		// 取余数对应的字符，放到数组头部（逆序构建）
		buf = append([]byte{alphabet[id%base]}, buf...)
		id /= base
	}
	return string(buf)
}

// Decode 将 Base62 字符串解码为 uint64 数字。
// 如果字符串包含不属于字符集的字符，返回 ErrInvalidBase62Char 错误。
//
// 用途：将短码还原为数字（如管理后台按 ID 查询）。
func Decode(s string) (uint64, error) {
	var n uint64
	for _, r := range s {
		pos := indexOf(alphabet, r)
		if pos < 0 {
			return 0, ErrInvalidBase62Char(r)
		}
		n = n*base + uint64(pos)
	}
	return n, nil
}

// indexOf 返回字符 r 在字符串 s 中的索引位置，未找到返回 -1。
// 这是一个内部辅助函数。
func indexOf(s string, r rune) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}

// IsValid 校验字符串是否为合法的 Base62 编码（全部字符都在字符集内且非空）。
//
// 调用位置：
//   - handler.Redirect()：校验 URL 中的短码格式
//   - service.Shorten()：校验用户自定义短码是否合法
func IsValid(s string) bool {
	for _, r := range s {
		if indexOf(alphabet, r) < 0 {
			return false
		}
	}
	return len(s) > 0
}

// ErrInvalidBase62Char 是解码时遇到非法字符返回的错误类型。
// 其底层类型是 rune，Error() 方法会打印出具体的非法字符。
type ErrInvalidBase62Char rune

func (e ErrInvalidBase62Char) Error() string {
	return "base62: invalid character: " + string(e)
}
