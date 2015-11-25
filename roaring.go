// Package roaring is an implementation of Roaring Bitmaps in Go.
// They provide fast compressed bitmap data structures (also called bitset).
// They are ideally suited to represent sets of integers over
// relatively small ranges.
// See http://roaringbitmap.org for details.
package roaring

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
)

// RoaringBitmap represents a compressed bitmap where you can add integers.
type RoaringBitmap struct {
	highlowcontainer roaringArray
}

func (b *RoaringBitmap) ToBase64() (string, error) {
	buf := new(bytes.Buffer)
	_, err := b.WriteTo(buf)
	return base64.StdEncoding.EncodeToString(buf.Bytes()), err

}

func (b *RoaringBitmap) FromBase64(str string) (int, error) {

	data, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return 0, err
	}
	buf := bytes.NewBuffer(data)

	return b.ReadFrom(buf)

}

// Write out a serialized version of this bitmap to stream
func (b *RoaringBitmap) WriteTo(stream io.Writer) (int, error) {
	return b.highlowcontainer.writeTo(stream)
}

// Read a serialized version of this bitmap from stream
func (b *RoaringBitmap) ReadFrom(stream io.Reader) (int, error) {
	return b.highlowcontainer.readFrom(stream)
}

// NewRoaringBitmap creates a new empty RoaringBitmap
func NewRoaringBitmap() *RoaringBitmap {
	return &RoaringBitmap{*newRoaringArray()}
}

// Clear removes all content from the RoaringBitmap and frees the memory
func (rb *RoaringBitmap) Clear() {
	rb.highlowcontainer = *newRoaringArray()
}

// ToArray creates a new slice containing all of the integers stored in the RoaringBitmap in sorted order
func (rb *RoaringBitmap) ToArray() []uint32 {
	array := make([]uint32, rb.GetCardinality())
	pos := 0
	pos2 := 0

	for pos < rb.highlowcontainer.size() {
		hs := toIntUnsigned(rb.highlowcontainer.getKeyAtIndex(pos)) << 16
		c := rb.highlowcontainer.getContainerAtIndex(pos)
		pos++
		c.fillLeastSignificant16bits(array, pos2, hs)
		pos2 += c.getCardinality()
	}
	return array
}

// GetSizeInBytes estimates the memory usage of the RoaringBitmap. Note that this
// might differ slightly from the amount of bytes required for persistent storage
func (rb *RoaringBitmap) GetSizeInBytes() uint64 {
	size := uint64(8)
	for _, c := range rb.highlowcontainer.containers {
		size += uint64(2) + uint64(c.getSizeInBytes())
	}
	return size
}

// GetSerializedSizeInBytes computes the serialized size in bytes  the RoaringBitmap. It should correspond to the
// number of bytes written when invoking WriteTo
func (rb *RoaringBitmap) GetSerializedSizeInBytes() uint64 {
	return rb.highlowcontainer.serializedSizeInBytes()
}

// IntIterable allows you to iterate over the values in a RoaringBitmap
type IntIterable interface {
	HasNext() bool
	Next() uint32
}

type intIterator struct {
	pos              int
	hs               uint32
	iter             shortIterable
	highlowcontainer *roaringArray
}

// HasNext returns true if there are more integers to iterate over
func (ii *intIterator) HasNext() bool {
	return ii.pos < ii.highlowcontainer.size()
}

func (ii *intIterator) init() {
	if ii.highlowcontainer.size() > ii.pos {
		ii.iter = ii.highlowcontainer.getContainerAtIndex(ii.pos).getShortIterator()
		ii.hs = toIntUnsigned(ii.highlowcontainer.getKeyAtIndex(ii.pos)) << 16
	}
}

// Next returns the next integer
func (ii *intIterator) Next() uint32 {
	x := toIntUnsigned(ii.iter.next()) | ii.hs
	if !ii.iter.hasNext() {
		ii.pos = ii.pos + 1
		ii.init()
	}
	return x
}

func newIntIterator(a *RoaringBitmap) *intIterator {
	p := new(intIterator)
	p.pos = 0
	p.highlowcontainer = &a.highlowcontainer
	p.init()
	return p
}

// String creates a string representation of the RoaringBitmap
func (rb *RoaringBitmap) String() string {
	// inspired by https://github.com/fzandona/goroar/blob/master/roaringbitmap.go
	var buffer bytes.Buffer
	start := []byte("{")
	buffer.Write(start)
	i := rb.Iterator()
	for i.HasNext() {
		buffer.WriteString(strconv.Itoa(int(i.Next())))
		if i.HasNext() { // todo: optimize
			buffer.WriteString(",")
		}
	}
	buffer.WriteString("}")
	return buffer.String()
}

// Iterator creates a new IntIterable to iterate over the integers contained in the bitmap, in sorted order
func (rb *RoaringBitmap) Iterator() IntIterable {
	return newIntIterator(rb)
}

// Clone creates a copy of the RoaringBitmap
func (rb *RoaringBitmap) Clone() *RoaringBitmap {
	ptr := new(RoaringBitmap)
	ptr.highlowcontainer = *rb.highlowcontainer.clone()
	return ptr
}

// Contains returns true if the integer is contained in the bitmap
func (rb *RoaringBitmap) Contains(x uint32) bool {
	hb := highbits(x)
	c := rb.highlowcontainer.getContainer(hb)
	return c != nil && c.contains(lowbits(x))
}

// Contains returns true if the integer is contained in the bitmap (this is a convenience method, the parameter is casted to uint32 and Contains is called)
func (rb *RoaringBitmap) ContainsInt(x int) bool {
	return rb.Contains(uint32(x))
}

// Equals returns true if the two bitmaps contain the same integers
func (rb *RoaringBitmap) Equals(o interface{}) bool {
	srb, ok := o.(*RoaringBitmap)
	if ok {
		return srb.highlowcontainer.equals(rb.highlowcontainer)
	}
	return false
}

// Add the integer x to the bitmap
func (rb *RoaringBitmap) Add(x uint32) {
	hb := highbits(x)
	ra := &rb.highlowcontainer
	i := ra.getIndex(hb)
	if i >= 0 {
		var c container
		if ra.hasDirty() {
			c = ra.getWritableContainerAtIndex(i)
		} else {
			c = ra.getContainerAtIndex(i)
		}
		c = c.add(lowbits(x))
		rb.highlowcontainer.setContainerAtIndex(i, c)
	} else {
		newac := newArrayContainer()
		rb.highlowcontainer.insertNewKeyValueAt(-i-1, hb, newac.add(lowbits(x)))
	}
}

// Add the integer x to the bitmap and return true  if it was added (false if the integer was already present)
func (rb *RoaringBitmap) CheckedAdd(x uint32) bool {
	// TODO: add unit tests for this method
	hb := highbits(x)
	i := rb.highlowcontainer.getIndex(hb)
	if i >= 0 {
		C := rb.highlowcontainer.getContainerAtIndex(i)
		oldcard := C.getCardinality()
		C = C.add(lowbits(x))
		rb.highlowcontainer.setContainerAtIndex(i, C)
		return C.getCardinality() > oldcard
	} else {
		newac := newArrayContainer()
		rb.highlowcontainer.insertNewKeyValueAt(-i-1, hb, newac.add(lowbits(x)))
		return true
	}
}

// Add the integer x to the bitmap (convenience method: the parameter is casted to uint32 and we call Add)
func (rb *RoaringBitmap) AddInt(x int) {
	rb.Add(uint32(x))
}

// Remove the integer x from the bitmap
func (rb *RoaringBitmap) Remove(x uint32) {
	hb := highbits(x)
	i := rb.highlowcontainer.getIndex(hb)
	if i >= 0 {
		c := rb.highlowcontainer.getWritableContainerAtIndex(i).remove(lowbits(x))
		rb.highlowcontainer.setContainerAtIndex(i, c.remove(lowbits(x)))
		if rb.highlowcontainer.getContainerAtIndex(i).getCardinality() == 0 {
			rb.highlowcontainer.removeAtIndex(i)
		}
	}
}

// Remove the integer x from the bitmap and return true if the integer was effectively remove (and false if the integer was not present)
func (rb *RoaringBitmap) CheckedRemove(x uint32) bool {
	// TODO: add unit tests for this method
	hb := highbits(x)
	i := rb.highlowcontainer.getIndex(hb)
	if i >= 0 {
		C := rb.highlowcontainer.getContainerAtIndex(i)
		oldcard := C.getCardinality()
		C = C.remove(lowbits(x))
		rb.highlowcontainer.setContainerAtIndex(i, C)
		if rb.highlowcontainer.getContainerAtIndex(i).getCardinality() == 0 {
			rb.highlowcontainer.removeAtIndex(i)
			return true
		}
		return C.getCardinality() < oldcard
	} else {
		return false
	}
}

// IsEmpty returns true if the RoaringBitmap is empty (it is faster than doing (GetCardinality() == 0))
func (rb *RoaringBitmap) IsEmpty() bool {
	return rb.highlowcontainer.size() == 0
}

// GetCardinality returns the number of integers contained in the bitmap
func (rb *RoaringBitmap) GetCardinality() uint64 {
	size := uint64(0)
	for _, c := range rb.highlowcontainer.containers {
		size += uint64(c.getCardinality())
	}
	return size
}

// Rank returns the number of integers that are smaller or equal to x (Rank(infinity) would be GetCardinality())
func (rb *RoaringBitmap) Rank(x uint32) uint32 {
	size := uint32(0)
	for i := 0; i < rb.highlowcontainer.size(); i++ {
		key := rb.highlowcontainer.getKeyAtIndex(i)
		if key > highbits(x) {
			return size
		}
		if key < highbits(x) {
			size += uint32(rb.highlowcontainer.getContainerAtIndex(i).getCardinality())
		} else {
			return size + uint32(rb.highlowcontainer.getContainerAtIndex(i).rank(lowbits(x)))
		}
	}
	return size
}

// Select returns the xth integer in the bitmap
func (rb *RoaringBitmap) Select(x uint32) (uint32, error) {
	if rb.GetCardinality() <= uint64(x) {
		return 0, fmt.Errorf("Can't find %dth integer in a bitmap with only %d items", x, rb.GetCardinality())
	}

	remaining := x
	for i := 0; i < rb.highlowcontainer.size(); i++ {
		c := rb.highlowcontainer.getContainerAtIndex(i)
		if remaining >= uint32(c.getCardinality()) {
			remaining -= uint32(c.getCardinality())
		} else {
			key := rb.highlowcontainer.getKeyAtIndex(i)
			return uint32(key)<<16 + uint32(c.selectInt(uint16(remaining))), nil
		}
	}
	return 0, fmt.Errorf("Can't find %dth integer in a bitmap with only %d items", x, rb.GetCardinality())
}

// And computes the intersection between two bitmaps and stores the result in the current bitmap
func (rb *RoaringBitmap) And(x2 *RoaringBitmap) {
	pos1 := 0
	pos2 := 0
	intersectionsize := 0
	length1 := rb.highlowcontainer.size()
	length2 := x2.highlowcontainer.size()

main:
	for {
		if pos1 < length1 && pos2 < length2 {
			s1 := rb.highlowcontainer.getKeyAtIndex(pos1)
			s2 := x2.highlowcontainer.getKeyAtIndex(pos2)
			for {
				if s1 == s2 {
					c1 := rb.highlowcontainer.getWritableContainerAtIndex(pos1)
					c2 := x2.highlowcontainer.getContainerAtIndex(pos2)
					diff := c1.iand(c2)
					if diff.getCardinality() > 0 {
						rb.highlowcontainer.replaceKeyAndContainerAtIndex(intersectionsize, s1, diff)
						intersectionsize++
					}
					pos1++
					pos2++
					if (pos1 == length1) || (pos2 == length2) {
						break main
					}
					s1 = rb.highlowcontainer.getKeyAtIndex(pos1)
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				} else if s1 < s2 {
					pos1 = rb.highlowcontainer.advanceUntil(s2, pos1)
					if pos1 == length1 {
						break main
					}
					s1 = rb.highlowcontainer.getKeyAtIndex(pos1)
				} else { //s1 > s2
					pos2 = x2.highlowcontainer.advanceUntil(s1, pos2)
					if pos2 == length2 {
						break main
					}
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				}
			}
		} else {
			break
		}
	}
	rb.highlowcontainer.resize(intersectionsize)
}

// OrCardinality  returns the cardinality of the union between two bitmaps, bitmaps are not modified
func (x1 *RoaringBitmap) OrCardinality(x2 *RoaringBitmap) uint64 {
	pos1 := 0
	pos2 := 0
	length1 := x1.highlowcontainer.size()
	length2 := x2.highlowcontainer.size()
	answer := uint64(0)
main:
	for {
		if (pos1 < length1) && (pos2 < length2) {
			s1 := x1.highlowcontainer.getKeyAtIndex(pos1)
			s2 := x2.highlowcontainer.getKeyAtIndex(pos2)

			for {
				if s1 < s2 {
					answer += x1.highlowcontainer.getContainerAtIndex(pos1).getCardinality()
					pos1++
					if pos1 == length1 {
						break main
					}
					s1 = x1.highlowcontainer.getKeyAtIndex(pos1)
				} else if s1 > s2 {
					answer += x2.highlowcontainer.getContainerAtIndex(pos2).getCardinality()
					pos2++
					if pos2 == length2 {
						break main
					}
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				} else {
					// TODO: could be faster if we did not have to materialize the container
					answer += x1.highlowcontainer.getContainerAtIndex(pos1).or(x2.highlowcontainer.getContainerAtIndex(pos2)).getCardinality()
					pos1++
					pos2++
					if (pos1 == length1) || (pos2 == length2) {
						break main
					}
					s1 = x1.highlowcontainer.getKeyAtIndex(pos1)
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				}
			}
		} else {
			break
		}
	}
	for ; pos1 < length1; pos1++ {
		answer += x1.highlowcontainer.getContainerAtIndex(pos1).getCardinality()
	}
	for ; pos2 < length2; pos2++ {
		answer += x2.highlowcontainer.getContainerAtIndex(pos2).getCardinality()
	}
	return answer
}

// AndCardinality returns the cardinality of the intersection between two bitmaps, bitmaps are not modified
func (rb *RoaringBitmap) AndCardinality(x2 *RoaringBitmap) {
	pos1 := 0
	pos2 := 0
	answer := uint64(0)
	length1 := rb.highlowcontainer.size()
	length2 := x2.highlowcontainer.size()

main:
	for {
		if pos1 < length1 && pos2 < length2 {
			s1 := rb.highlowcontainer.getKeyAtIndex(pos1)
			s2 := x2.highlowcontainer.getKeyAtIndex(pos2)
			for {
				if s1 == s2 {
					c1 := rb.highlowcontainer.getWritableContainerAtIndex(pos1)
					c2 := x2.highlowcontainer.getContainerAtIndex(pos2)
					diff := c1.and(c2)
					answer += diff.getCardinality() // TODO: could be faster if we did not have to compute diff
					pos1++
					pos2++
					if (pos1 == length1) || (pos2 == length2) {
						break main
					}
					s1 = rb.highlowcontainer.getKeyAtIndex(pos1)
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				} else if s1 < s2 {
					pos1 = rb.highlowcontainer.advanceUntil(s2, pos1)
					if pos1 == length1 {
						break main
					}
					s1 = rb.highlowcontainer.getKeyAtIndex(pos1)
				} else { //s1 > s2
					pos2 = x2.highlowcontainer.advanceUntil(s1, pos2)
					if pos2 == length2 {
						break main
					}
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				}
			}
		} else {
			break
		}
	}
	return answer
}

// Intersects checks whether two bitmap intersects, bitmaps are not modified
func (rb *RoaringBitmap) Intersects(x2 *RoaringBitmap) bool {
	pos1 := 0
	pos2 := 0
	length1 := rb.highlowcontainer.size()
	length2 := x2.highlowcontainer.size()

main:
	for {
		if pos1 < length1 && pos2 < length2 {
			s1 := rb.highlowcontainer.getKeyAtIndex(pos1)
			s2 := x2.highlowcontainer.getKeyAtIndex(pos2)
			for {
				if s1 == s2 {
					c1 := rb.highlowcontainer.getContainerAtIndex(pos1)
					c2 := x2.highlowcontainer.getContainerAtIndex(pos2)
					if c1.intersects(c2) {
						return true
					}
					pos1++
					pos2++
					if (pos1 == length1) || (pos2 == length2) {
						break main
					}
					s1 = rb.highlowcontainer.getKeyAtIndex(pos1)
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				} else if s1 < s2 {
					pos1 = rb.highlowcontainer.advanceUntil(s2, pos1)
					if pos1 == length1 {
						break main
					}
					s1 = rb.highlowcontainer.getKeyAtIndex(pos1)
				} else { //s1 > s2
					pos2 = x2.highlowcontainer.advanceUntil(s1, pos2)
					if pos2 == length2 {
						break main
					}
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				}
			}
		} else {
			break
		}
	}
	return false
}

// Xor computes the symmetric difference between two bitmaps and stores the result in the current bitmap
func (rb *RoaringBitmap) Xor(x2 *RoaringBitmap) {
	results := Xor(rb, x2) // Todo: could be computed in-place for reduced memory usage
	rb.highlowcontainer = results.highlowcontainer
}

// Or computes the union between two bitmaps and stores the result in the current bitmap
func (rb *RoaringBitmap) Or(x2 *RoaringBitmap) {
	results := Or(rb, x2) // Todo: could be computed in-place for reduced memory usage
	rb.highlowcontainer = results.highlowcontainer
}

// AndNot computes the difference between two bitmaps and stores the result in the current bitmap
func (rb *RoaringBitmap) AndNot(x2 *RoaringBitmap) {
	pos1 := 0
	pos2 := 0
	intersectionsize := 0
	length1 := rb.highlowcontainer.size()
	length2 := x2.highlowcontainer.size()

main:
	for {
		if pos1 < length1 && pos2 < length2 {
			s1 := rb.highlowcontainer.getKeyAtIndex(pos1)
			s2 := x2.highlowcontainer.getKeyAtIndex(pos2)
			for {
				if s1 == s2 {
					c1 := rb.highlowcontainer.getWritableContainerAtIndex(pos1)
					c2 := x2.highlowcontainer.getContainerAtIndex(pos2)
					diff := c1.iandNot(c2)
					if diff.getCardinality() > 0 {
						rb.highlowcontainer.replaceKeyAndContainerAtIndex(intersectionsize, s1, diff)
						intersectionsize++
					}
					pos1++
					pos2++
					if (pos1 == length1) || (pos2 == length2) {
						break main
					}
					s1 = rb.highlowcontainer.getKeyAtIndex(pos1)
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				} else if s1 < s2 {
					c1 := rb.highlowcontainer.getWritableContainerAtIndex(pos1)
					rb.highlowcontainer.replaceKeyAndContainerAtIndex(intersectionsize, s1, c1)
					intersectionsize++
					pos1++
					if pos1 == length1 {
						break main
					}
					s1 = rb.highlowcontainer.getKeyAtIndex(pos1)
				} else { //s1 > s2
					pos2 = x2.highlowcontainer.advanceUntil(s1, pos2)
					if pos2 == length2 {
						break main
					}
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				}
			}
		} else {
			break
		}
	}
	// TODO:implement as a copy
	for pos1 < length1 {
		c1 := rb.highlowcontainer.getContainerAtIndex(pos1)
		s1 := rb.highlowcontainer.getKeyAtIndex(pos1)
		rb.highlowcontainer.replaceKeyAndContainerAtIndex(intersectionsize, s1, c1)
		intersectionsize++
		pos1++
	}
	rb.highlowcontainer.resize(intersectionsize)
}

// Or computes the union between two bitmaps and returns the result
func Or(x1, x2 *RoaringBitmap) *RoaringBitmap {
	answer := NewRoaringBitmap()
	pos1 := 0
	pos2 := 0
	length1 := x1.highlowcontainer.size()
	length2 := x2.highlowcontainer.size()
main:
	for {
		if (pos1 < length1) && (pos2 < length2) {
			s1 := x1.highlowcontainer.getKeyAtIndex(pos1)
			s2 := x2.highlowcontainer.getKeyAtIndex(pos2)

			for {
				if s1 < s2 {
					answer.highlowcontainer.appendCopy(x1.highlowcontainer, pos1)
					pos1++
					if pos1 == length1 {
						break main
					}
					s1 = x1.highlowcontainer.getKeyAtIndex(pos1)
				} else if s1 > s2 {
					answer.highlowcontainer.appendCopy(x2.highlowcontainer, pos2)
					pos2++
					if pos2 == length2 {
						break main
					}
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				} else {

					answer.highlowcontainer.appendContainer(s1, x1.highlowcontainer.getContainerAtIndex(pos1).or(x2.highlowcontainer.getContainerAtIndex(pos2)))
					pos1++
					pos2++
					if (pos1 == length1) || (pos2 == length2) {
						break main
					}
					s1 = x1.highlowcontainer.getKeyAtIndex(pos1)
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				}
			}
		} else {
			break
		}
	}
	if pos1 == length1 {
		answer.highlowcontainer.appendCopyMany(x2.highlowcontainer, pos2, length2)
	} else if pos2 == length2 {
		answer.highlowcontainer.appendCopyMany(x1.highlowcontainer, pos1, length1)
	}
	return answer
}

// And computes the intersection between two bitmaps and returns the result
func And(x1, x2 *RoaringBitmap) *RoaringBitmap {
	answer := NewRoaringBitmap()
	pos1 := 0
	pos2 := 0
	length1 := x1.highlowcontainer.size()
	length2 := x2.highlowcontainer.size()
main:
	for {
		if pos1 < length1 && pos2 < length2 {
			s1 := x1.highlowcontainer.getKeyAtIndex(pos1)
			s2 := x2.highlowcontainer.getKeyAtIndex(pos2)
			for {
				if s1 == s2 {
					C := x1.highlowcontainer.getContainerAtIndex(pos1)
					C = C.and(x2.highlowcontainer.getContainerAtIndex(pos2))

					if C.getCardinality() > 0 {
						answer.highlowcontainer.appendContainer(s1, C)
					}
					pos1++
					pos2++
					if (pos1 == length1) || (pos2 == length2) {
						break main
					}
					s1 = x1.highlowcontainer.getKeyAtIndex(pos1)
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				} else if s1 < s2 {
					pos1 = x1.highlowcontainer.advanceUntil(s2, pos1)
					if pos1 == length1 {
						break main
					}
					s1 = x1.highlowcontainer.getKeyAtIndex(pos1)
				} else { // s1 > s2
					pos2 = x2.highlowcontainer.advanceUntil(s1, pos2)
					if pos2 == length2 {
						break main
					}
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				}
			}
		} else {
			break
		}
	}
	return answer
}

// Xor computes the symmetric difference between two bitmaps and returns the result
func Xor(x1, x2 *RoaringBitmap) *RoaringBitmap {
	answer := NewRoaringBitmap()
	pos1 := 0
	pos2 := 0
	length1 := x1.highlowcontainer.size()
	length2 := x2.highlowcontainer.size()
	for {
		if (pos1 < length1) && (pos2 < length2) {
			s1 := x1.highlowcontainer.getKeyAtIndex(pos1)
			s2 := x2.highlowcontainer.getKeyAtIndex(pos2)
			if s1 < s2 {
				answer.highlowcontainer.appendCopy(x1.highlowcontainer, pos1)
				pos1++
			} else if s1 > s2 {
				answer.highlowcontainer.appendCopy(x2.highlowcontainer, pos2)
				pos2++
			} else {
				c := x1.highlowcontainer.getContainerAtIndex(pos1).xor(x2.highlowcontainer.getContainerAtIndex(pos2))
				if c.getCardinality() > 0 {
					answer.highlowcontainer.appendContainer(s1, c)
				}
				pos1++
				pos2++
			}
		} else {
			break
		}
	}
	if pos1 == length1 {
		answer.highlowcontainer.appendCopyMany(x2.highlowcontainer, pos2, length2)
	} else if pos2 == length2 {
		answer.highlowcontainer.appendCopyMany(x1.highlowcontainer, pos1, length1)
	}
	return answer
}

// AndNot computes the difference between two bitmaps and returns the result
func AndNot(x1, x2 *RoaringBitmap) *RoaringBitmap {
	answer := NewRoaringBitmap()
	pos1 := 0
	pos2 := 0
	length1 := x1.highlowcontainer.size()
	length2 := x2.highlowcontainer.size()

main:
	for {
		if pos1 < length1 && pos2 < length2 {
			s1 := x1.highlowcontainer.getKeyAtIndex(pos1)
			s2 := x2.highlowcontainer.getKeyAtIndex(pos2)
			for {
				if s1 < s2 {
					answer.highlowcontainer.appendCopy(x1.highlowcontainer, pos1)
					pos1++
					if pos1 == length1 {
						break main
					}
					s1 = x1.highlowcontainer.getKeyAtIndex(pos1)
				} else if s1 == s2 {
					c1 := x1.highlowcontainer.getContainerAtIndex(pos1)
					c2 := x2.highlowcontainer.getContainerAtIndex(pos2)
					diff := c1.andNot(c2)
					if diff.getCardinality() > 0 {
						answer.highlowcontainer.appendContainer(s1, diff)
					}
					pos1++
					pos2++
					if (pos1 == length1) || (pos2 == length2) {
						break main
					}
					s1 = x1.highlowcontainer.getKeyAtIndex(pos1)
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				} else { //s1 > s2
					pos2 = x2.highlowcontainer.advanceUntil(s1, pos2)
					if pos2 == length2 {
						break main
					}
					s2 = x2.highlowcontainer.getKeyAtIndex(pos2)
				}
			}
		} else {
			break
		}
	}
	if pos2 == length2 {
		answer.highlowcontainer.appendCopyMany(x1.highlowcontainer, pos1, length1)
	}
	return answer
}

// BitmapOf generates a new bitmap filled with the specified integer
func BitmapOf(dat ...uint32) *RoaringBitmap {
	ans := NewRoaringBitmap()
	for _, i := range dat {
		ans.Add(i)
	}
	return ans
}

// Flip negates the bits in the given range, any integer present in this range and in the bitmap is removed,
// and any integer present in the range and not in the bitmap is added
func (rb *RoaringBitmap) Flip(rangeStart, rangeEnd uint32) {

	if rangeStart >= rangeEnd {
		return
	}

	hbStart := highbits(rangeStart)
	lbStart := lowbits(rangeStart)
	hbLast := highbits(rangeEnd - 1)
	lbLast := lowbits(rangeEnd - 1)

	max := toIntUnsigned(maxLowBit())
	for hb := hbStart; hb <= hbLast; hb++ {
		containerStart := uint32(0)
		if hb == hbStart {
			containerStart = toIntUnsigned(lbStart)
		}
		containerLast := max
		if hb == hbLast {
			containerLast = toIntUnsigned(lbLast)
		}

		i := rb.highlowcontainer.getIndex(hb)

		if i >= 0 {
			c := rb.highlowcontainer.getWritableContainerAtIndex(i).inot(int(containerStart), int(containerLast))
			if c.getCardinality() > 0 {
				rb.highlowcontainer.setContainerAtIndex(i, c)
			} else {
				rb.highlowcontainer.removeAtIndex(i)
			}
		} else { // *think* the range of ones must never be
			// empty.
			rb.highlowcontainer.insertNewKeyValueAt(-i-1, hb, rangeOfOnes(int(containerStart), int(containerLast)))
		}
	}
}

// FlipInt calls Flip after casting the parameters to uint32 (convenience method)
func (rb *RoaringBitmap) FlipInt(rangeStart, rangeEnd int) {
	rb.Flip(uint32(rangeStart), uint32(rangeEnd))
}

// Add the integers in [rangeStart, rangeEnd) to the bitmap
func (rb *RoaringBitmap) AddRange(rangeStart, rangeEnd uint32) {
	if rangeStart >= rangeEnd {
		return
	}

	hbStart := toIntUnsigned(highbits(rangeStart))
	lbStart := toIntUnsigned(lowbits(rangeStart))
	hbLast := toIntUnsigned(highbits(rangeEnd - 1))
	lbLast := toIntUnsigned(lowbits(rangeEnd - 1))

	max := toIntUnsigned(maxLowBit())
	for hb := uint16(hbStart); hb <= uint16(hbLast); hb++ {
		containerStart := uint32(0)
		if hb == uint16(hbStart) {
			containerStart = lbStart
		}
		containerLast := max
		if hb == uint16(hbLast) {
			containerLast = lbLast
		}

		i := rb.highlowcontainer.getIndex(hb)

		if i >= 0 {
			c := rb.highlowcontainer.getContainerAtIndex(i).iaddRange(int(containerStart), int(containerLast+1))
			rb.highlowcontainer.setContainerAtIndex(i, c)
		} else { // *think* the range of ones must never be
			// empty.
			rb.highlowcontainer.insertNewKeyValueAt(-i-1, hb, rangeOfOnes(int(containerStart), int(containerLast)))
		}
	}
}

// Remove the integers in [rangeStart, rangeEnd) from the bitmap
func (rb *RoaringBitmap) RemoveRange(rangeStart, rangeEnd uint32) {
	if rangeStart >= rangeEnd {
		return
	}

	hbStart := toIntUnsigned(highbits(rangeStart))
	lbStart := toIntUnsigned(lowbits(rangeStart))
	hbLast := toIntUnsigned(highbits(rangeEnd - 1))
	lbLast := toIntUnsigned(lowbits(rangeEnd - 1))

	max := toIntUnsigned(maxLowBit())

	if hbStart == hbLast {
		i := rb.highlowcontainer.getIndex(uint16(hbStart))
		if i < 0 {
			return
		}
		c := rb.highlowcontainer.getContainerAtIndex(i).iremoveRange(int(lbStart), int(lbLast+1))
		if c.getCardinality() > 0 {
			rb.highlowcontainer.setContainerAtIndex(i, c)
		} else {
			rb.highlowcontainer.removeAtIndex(i)
		}
		return
	}
	ifirst := rb.highlowcontainer.getIndex(uint16(hbStart))
	ilast := rb.highlowcontainer.getIndex(uint16(hbLast))

	if ifirst >= 0 {
		if lbStart != 0 {
			c := rb.highlowcontainer.getContainerAtIndex(ifirst).iremoveRange(int(lbStart), int(max+1))
			if c.getCardinality() > 0 {
				rb.highlowcontainer.setContainerAtIndex(ifirst, c)
				ifirst++
			}
		}
	} else {
		ifirst = -ifirst - 1
	}
	if ilast >= 0 {
		if lbLast != max {
			c := rb.highlowcontainer.getContainerAtIndex(ilast).iremoveRange(int(0), int(lbLast+1))
			if c.getCardinality() > 0 {
				rb.highlowcontainer.setContainerAtIndex(ilast, c)
			} else {
				ilast++
			}
		} else {
			ilast++
		}
	} else {
		ilast = -ilast - 1
	}
	rb.highlowcontainer.removeIndexRange(ifirst, ilast)
}

// Flip negates the bits in the given range, any integer present in this range and in the bitmap is removed,
// and any integer present in the range and not in the bitmap is added, a new bitmap is returned leaving
// the current bitmap unchanged
func Flip(bm *RoaringBitmap, rangeStart, rangeEnd uint32) *RoaringBitmap {
	if rangeStart >= rangeEnd {
		return bm.Clone()
	}

	answer := NewRoaringBitmap()
	hbStart := highbits(rangeStart)
	lbStart := lowbits(rangeStart)
	hbLast := highbits(rangeEnd - 1)
	lbLast := lowbits(rangeEnd - 1)

	// copy the containers before the active area
	answer.highlowcontainer.appendCopiesUntil(bm.highlowcontainer, hbStart)

	max := toIntUnsigned(maxLowBit())
	for hb := hbStart; hb <= hbLast; hb++ {
		containerStart := uint32(0)
		if hb == hbStart {
			containerStart = toIntUnsigned(lbStart)
		}
		containerLast := max
		if hb == hbLast {
			containerLast = toIntUnsigned(lbLast)
		}

		i := bm.highlowcontainer.getIndex(hb)
		j := answer.highlowcontainer.getIndex(hb)

		if i >= 0 {
			c := bm.highlowcontainer.getContainerAtIndex(i).not(int(containerStart), int(containerLast))
			if c.getCardinality() > 0 {
				answer.highlowcontainer.insertNewKeyValueAt(-j-1, hb, c)
			}

		} else { // *think* the range of ones must never be
			// empty.
			answer.highlowcontainer.insertNewKeyValueAt(-j-1, hb,
				rangeOfOnes(int(containerStart), int(containerLast)))
		}
	}
	// copy the containers after the active area.
	answer.highlowcontainer.appendCopiesAfter(bm.highlowcontainer, hbLast)

	return answer
}

// FlipInt calls Flip after casting the parameters to uint32 (convenience method)
func FlipInt(bm *RoaringBitmap, rangeStart, rangeEnd int) *RoaringBitmap {
	return Flip(bm, uint32(rangeStart), uint32(rangeEnd))
}
