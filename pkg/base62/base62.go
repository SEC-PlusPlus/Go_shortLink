package base62

const (
	// alphabet is the set of characters used for Base62 encoding.
	alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	base     = uint64(len(alphabet))
)

// Encode converts a uint64 number into a Base62 string.
// This is used to convert the auto-increment counter into a short code.
func Encode(id uint64) string {
	if id == 0 {
		return string(alphabet[0])
	}

	var buf []byte
	for id > 0 {
		buf = append([]byte{alphabet[id%base]}, buf...)
		id /= base
	}
	return string(buf)
}

// Decode converts a Base62 string back into a uint64 number.
// Returns an error if the string contains characters not in the alphabet.
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

// indexOf returns the index of rune r in string s, or -1 if not found.
func indexOf(s string, r rune) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}

// IsValid checks if a string is a valid Base62-encoded short code.
func IsValid(s string) bool {
	for _, r := range s {
		if indexOf(alphabet, r) < 0 {
			return false
		}
	}
	return len(s) > 0
}

// ErrInvalidBase62Char is returned when a invalid character is encountered.
type ErrInvalidBase62Char rune

func (e ErrInvalidBase62Char) Error() string {
	return "base62: invalid character: " + string(e)
}
