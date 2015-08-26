// Package also implements RFC-6901, JSON pointers.
// Pointers themself can be encoded into cbor format and
// vice-versa.
//
//   cbor-path :    | Text-chunk-start |
//                        | tagJsonString | len | segment1 |
//                        | tagJsonString | len | segment2 |
//                        ...
//                  | Break-stop |
package cbor

import "strconv"
import "bytes"

//import "fmt"
import "errors"

// ErrorInvalidArrayOffset
var ErrorInvalidArrayOffset = errors.New("cbor.invalidArrayOffset")

// ErrorInvalidPointer
var ErrorInvalidPointer = errors.New("cbor.invalidPointer")

// ErrorNoKey
var ErrorNoKey = errors.New("cbor.noKey")

// ErrorExpectedKey
var ErrorExpectedKey = errors.New("cbor.expectedKey")

// ErrorExpectedCborKey
var ErrorExpectedCborKey = errors.New("cbor.expectedCborKey")

// ErrorMalformedDocument
var ErrorMalformedDocument = errors.New("cbor.malformedDocument")

// ErrorInvalidDocument
var ErrorInvalidDocument = errors.New("cbor.invalidDocument")

// ErrorUnknownType to encode
var ErrorUnknownType = errors.New("cbor.unknownType")

const maxPartSize int = 1024

func fromJsonPointer(jsonptr, out []byte) int {
	var part [maxPartSize]byte

	n, off := encodeTextStart(out), 0
	for i := 0; i < len(jsonptr); {
		if jsonptr[i] == '~' {
			if jsonptr[i+1] == '1' {
				part[off] = '/'
				off, i = off+1, i+2

			} else if jsonptr[i+1] == '0' {
				part[off] = '~'
				off, i = off+1, i+2
			}

		} else if jsonptr[i] == '/' {
			if off > 0 {
				n += encodeTag(uint64(tagJsonString), out[n:])
				n += encodeText(bytes2str(part[:off]), out[n:])
				off = 0
			}
			i++

		} else {
			part[off] = jsonptr[i]
			i, off = i+1, off+1
		}
	}
	if off > 0 || (len(jsonptr) > 0 && jsonptr[len(jsonptr)-1] == '/') {
		n += encodeTag(uint64(tagJsonString), out[n:])
		n += encodeText(bytes2str(part[:off]), out[n:])
	}

	n += encodeBreakStop(out[n:])
	return n
}

func toJsonPointer(cborptr, out []byte) int {
	i, n := 1, 0
	for {
		if cborptr[i] == hdr(type6, info24) && cborptr[i+1] == tagJsonString {
			i, out[n] = i+2, '/'
			n += 1
			ln, j := decodeLength(cborptr[i:])
			ln, i = ln+i+j, i+j
			for i < ln {
				switch cborptr[i] {
				case '/':
					out[n], out[n+1] = '~', '1'
					n += 2
				case '~':
					out[n], out[n+1] = '~', '0'
					n += 2
				default:
					out[n] = cborptr[i]
					n += 1
				}
				i++
			}
		}
		if cborptr[i] == brkstp {
			break
		}
	}
	return n
}

func containerLen(doc []byte) (mjr byte, n int) {
	n = 0
	mjr, inf := major(doc[n]), info(doc[n])
	if mjr == type4 || mjr == type5 {
		if inf == indefiniteLength {
			return mjr, 1
		}
		panic("cbor pointer len-prefix not supported")
	}
	panic(ErrorMalformedDocument)
}

func partial(part, doc []byte) (start, end int, key bool) {
	var err error
	var index int
	mjr, n := containerLen(doc)
	//fmt.Println("partial", string(part), len(doc), n, doc)
	if mjr == type4 { // array
		if index, err = strconv.Atoi(bytes2str(part)); err != nil {
			panic(ErrorInvalidArrayOffset)
		}
		n += arrayIndex(doc[n:], index)
		m := itemsEnd(doc[n:])
		//fmt.Println("partial-arr", index, n, n+m, doc[n:n+m], string(part))
		return n, n + m, false

	} else if mjr == type5 { // map
		m, found := mapIndex(doc[n:], part)
		if !found { // key not found
			return n + m, n + m, found
		}
		n += m
		m = itemsEnd(doc[n:])    // key
		p := itemsEnd(doc[n+m:]) // value
		//fmt.Println("partial-map",n,n+m,n+m+p,doc[n+m:n+m+p],string(part),found)
		return n, n + m + p, found
	}
	panic(ErrorInvalidPointer)
}

func lookup(cborptr, doc []byte) (start, end int, key bool) {
	i, n, m := 1, 0, len(doc)
	start, end = n, m
	if i >= len(cborptr) || cborptr[i] == brkstp { // cborptr is empty ""
		return start, end, false
	}
	var k, keyln int
	for {
		doc = doc[n:m]
		if cborptr[i] != hdr(type6, info24) && cborptr[i+1] != tagJsonString {
			panic(ErrorInvalidPointer)
		}
		i += 2
		ln, j := decodeLength(cborptr[i:])
		n, m, key = partial(cborptr[i+j:i+j+ln], doc)
		i += j + ln
		start += n
		end = start + (m - n)
		if i >= len(cborptr) || cborptr[i] == brkstp {
			break
		}
		if key {
			if major(doc[n]) == type6 && doc[n+1] == tagJsonString {
				n, start = n+2, start+2
			}
			keyln, k = decodeLength(doc[n:])
			n, start = n+k+keyln, start+k+keyln
		}
	}
	return start, end, key
}

func arrayIndex(arr []byte, index int) int {
	count, prev, n := 0, 0, 0
	for arr[n] != brkstp {
		if count == index {
			return n
		} else if index >= 0 && arr[n] == brkstp {
			panic(ErrorInvalidArrayOffset)
		}
		prev = n
		n += itemsEnd(arr[n:])
		count++
	}
	if index == -1 && arr[n] == brkstp {
		return prev
	}
	panic(ErrorInvalidArrayOffset)
}

func mapIndex(buf []byte, part []byte) (int, bool) {
	n := 0
	for n < len(buf) {
		start := n
		if buf[n] == brkstp { // key-not-found
			return n + 1, false
		}
		// get key
		if major(buf[n]) == type6 && buf[n+1] == tagJsonString {
			n += 2
		}
		if major(buf[n]) != type3 {
			panic(ErrorExpectedKey)
		}
		ln, j := decodeLength(buf[n:])
		n += j
		m := n + ln
		//fmt.Println("mapIndex", n, m, string(buf[n:m]), start, part)
		if bytes.Compare(part, buf[n:m]) == 0 {
			return start, true
		}
		p := itemsEnd(buf[m:]) // value
		//fmt.Println("mapIndex", n, m, p, string(buf[n:m]), start)
		n = m + p
	}
	panic(ErrorMalformedDocument)
}

func itemsEnd(buf []byte) int {
	mjr, inf := major(buf[0]), info(buf[0])
	if mjr == type0 || mjr == type1 { // integer item
		if inf < info24 {
			return 1
		}
		return (1 << (inf - info24)) + 1

	} else if mjr == type3 { // string item
		ln, j := decodeLength(buf)
		return j + ln

	} else if mjr == type4 { // array item
		_, n := containerLen(buf)
		//fmt.Println("itemIndex-arr", size, n)
		if buf[n] == brkstp {
			return n + 1
		}
		n += arrayIndex(buf[n:], -1)
		return n + itemsEnd(buf[n:]) + 1 // skip brkstp

	} else if mjr == type5 { // map item
		_, n := containerLen(buf)
		//fmt.Println("itemIndex-map", size, n)
		for n < len(buf) {
			if buf[n] == brkstp {
				return n + 1
			}
			n += itemsEnd(buf[n:]) // key
			n += itemsEnd(buf[n:]) // value
		}

	} else if mjr == type7 {
		if inf == simpleTypeNil || inf == simpleTypeFalse ||
			inf == simpleTypeTrue {
			return 1
		} else if inf == flt32 { // item float32
			return 1 + 4
		} else if inf == flt64 { // item float64
			return 1 + 8
		}
		panic(ErrorInvalidDocument)

	} else if mjr == type6 && buf[1] == tagJsonString && major(buf[2]) == type3 {
		ln, j := decodeLength(buf[2:])
		return 2 + j + ln
	}
	//fmt.Println(buf)
	panic(ErrorInvalidDocument)
}

func skipKey(doc []byte) int {
	if major(doc[0]) == type6 && doc[1] == tagJsonString {
		ln, j := decodeLength(doc[2:])
		return 2 + j + ln
	}
	ln, j := decodeLength(doc)
	return j + ln
}

func get(doc, cborptr, item []byte) int {
	n, m, key := lookup(cborptr, doc)
	if n == m {
		panic(ErrorNoKey)
	} else if key { // if lookup in into a map, skip key
		n += skipKey(doc[n:])
	}
	copy(item, doc[n:m])
	return m - n
}

func set(doc, cborptr, item, newdoc, old []byte) (int, int) {
	n, m, key := lookup(cborptr, doc)
	if key {
		n += skipKey(doc[n:])
	}
	ln := len(item)
	copy(newdoc, doc[:n])
	copy(newdoc[n:], item)
	copy(newdoc[n+ln:], doc[m:])
	copy(old, doc[n:m])
	return (n + ln + len(doc[m:])), m - n
}

func prepend(doc, cborptr, item, newdoc []byte, config *Config) int {
	n, _, key := lookup(cborptr, doc)
	if key { // n points to {key,value} pair
		n += skipKey(doc[n:])
	}
	// n now points to value which can be an array or map.
	mjr := major(doc[n])
	if mjr != type4 && mjr != type5 {
		panic(ErrorInvalidPointer)
	}
	// copy every thing before value
	n++ // including mjr+indefiniteLength
	copy(newdoc, doc[:n])
	ln := len(item)
	copy(newdoc[n:], item)
	copy(newdoc[n+ln:], doc[n:])
	return n + ln + len(doc[n:])
}

func del(doc, cborptr, newdoc, deleted []byte) (int, int) {
	n, m, key := lookup(cborptr, doc)
	copy(newdoc, doc[:n])
	copy(newdoc[n:], doc[m:])
	// copy deleted value to o/p buffer.
	p := n
	if key {
		p += skipKey(doc[n:])
	}
	copy(deleted, doc[p:m])
	return n + len(doc[m:]), m - p
}
