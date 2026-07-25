package plumbing

// Adapted from https://github.com/kr/binarydist
// Original license:
//
// Copyright 2012 Keith Rarick
//
// Permission is hereby granted, free of charge, to any person
// obtaining a copy of this software and associated documentation
// files (the "Software"), to deal in the Software without
// restriction, including without limitation the rights to use,
// copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the
// Software is furnished to do so, subject to the following
// conditions:
//
// The above copyright notice and this permission notice shall be
// included in all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
// EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES
// OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
// NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT
// HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY,
// WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
// FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
// OTHER DEALINGS IN THE SOFTWARE.

import (
	"bytes"
)

func swap(a []int, i, j int) { a[i], a[j] = a[j], a[i] }

//nolint:funlen // Keep the partition steps aligned with the upstream binarydist algorithm.
func split(indexes, values []int, start, length, offset int) {
	var currentIndex, equalCount, groupIndex, pivotValue, lowerBound, upperBound int

	if length < 16 {
		for groupIndex = start; groupIndex < start+length; groupIndex += equalCount {
			equalCount = 1

			pivotValue = values[indexes[groupIndex]+offset]
			for currentIndex = 1; groupIndex+currentIndex < start+length; currentIndex++ {
				if values[indexes[groupIndex+currentIndex]+offset] < pivotValue {
					pivotValue = values[indexes[groupIndex+currentIndex]+offset]
					equalCount = 0
				}

				if values[indexes[groupIndex+currentIndex]+offset] == pivotValue {
					swap(indexes, groupIndex+currentIndex, groupIndex+equalCount)
					equalCount++
				}
			}

			for currentIndex = range equalCount {
				values[indexes[groupIndex+currentIndex]] = groupIndex + equalCount - 1
			}

			if equalCount == 1 {
				indexes[groupIndex] = -1
			}
		}

		return
	}

	pivotValue = values[indexes[start+length/2]+offset]
	lowerBound = 0
	upperBound = 0

	for currentIndex = start; currentIndex < start+length; currentIndex++ {
		if values[indexes[currentIndex]+offset] < pivotValue {
			lowerBound++
		}

		if values[indexes[currentIndex]+offset] == pivotValue {
			upperBound++
		}
	}

	lowerBound += start
	upperBound += lowerBound

	currentIndex = start
	equalCount = 0
	groupIndex = 0

	for currentIndex < lowerBound {
		switch {
		case values[indexes[currentIndex]+offset] < pivotValue:
			currentIndex++
		case values[indexes[currentIndex]+offset] == pivotValue:
			swap(indexes, currentIndex, lowerBound+equalCount)
			equalCount++
		default:
			swap(indexes, currentIndex, upperBound+groupIndex)
			groupIndex++
		}
	}

	for lowerBound+equalCount < upperBound {
		if values[indexes[lowerBound+equalCount]+offset] == pivotValue {
			equalCount++
		} else {
			swap(indexes, lowerBound+equalCount, upperBound+groupIndex)
			groupIndex++
		}
	}

	if lowerBound > start {
		split(indexes, values, start, lowerBound-start, offset)
	}

	for currentIndex = range upperBound - lowerBound {
		values[indexes[lowerBound+currentIndex]] = upperBound - 1
	}

	if lowerBound == upperBound-1 {
		indexes[lowerBound] = -1
	}

	if start+length > upperBound {
		split(indexes, values, upperBound, start+length-upperBound, offset)
	}
}

//nolint:funlen // Keep suffix-array construction aligned with the upstream binarydist algorithm.
func qsufsort(obuf []byte) []int {
	var buckets [256]int
	var bucketIndex, offset int
	suffixArray := make([]int, len(obuf)+1)
	ranks := make([]int, len(obuf)+1)

	for _, c := range obuf {
		buckets[c]++
	}

	for bucketIndex = 1; bucketIndex < 256; bucketIndex++ {
		buckets[bucketIndex] += buckets[bucketIndex-1]
	}

	copy(buckets[1:], buckets[:])
	buckets[0] = 0

	for i, c := range obuf {
		buckets[c]++
		suffixArray[buckets[c]] = i
	}

	suffixArray[0] = len(obuf)
	for i, c := range obuf {
		ranks[i] = buckets[c]
	}

	ranks[len(obuf)] = 0

	for bucketIndex = 1; bucketIndex < 256; bucketIndex++ {
		if buckets[bucketIndex] == buckets[bucketIndex-1]+1 {
			suffixArray[buckets[bucketIndex]] = -1
		}
	}

	suffixArray[0] = -1

	for offset = 1; suffixArray[0] != -(len(obuf) + 1); offset += offset {
		var groupLength int

		for index := 0; index < len(obuf)+1; {
			if suffixArray[index] < 0 {
				groupLength -= suffixArray[index]
				index -= suffixArray[index]
			} else {
				if groupLength != 0 {
					suffixArray[index-groupLength] = -groupLength
				}

				groupLength = ranks[suffixArray[index]] + 1 - index
				split(suffixArray, ranks, index, groupLength, offset)
				index += groupLength
				groupLength = 0
			}
		}

		if groupLength != 0 {
			suffixArray[len(obuf)+1-groupLength] = -groupLength
		}
	}

	for index := range len(obuf) + 1 {
		suffixArray[ranks[index]] = index
	}

	return suffixArray
}

func matchlen(a, b []byte) int {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}

	return i
}

func search(index []int, obuf, nbuf []byte, start, end int) (int, int) {
	if end-start < 2 {
		startLength := matchlen(obuf[index[start]:], nbuf)
		endLength := matchlen(obuf[index[end]:], nbuf)

		if startLength > endLength {
			return index[start], startLength
		}

		return index[end], endLength
	}

	middle := start + (end-start)/2
	if bytes.Compare(obuf[index[middle]:], nbuf) < 0 {
		return search(index, obuf, nbuf, middle, end)
	}

	return search(index, obuf, nbuf, start, middle)
}

// DiffBytes calculates the approximated number of different bytes between two binary buffers.
// We are not interested in the diff script itself. Instead, we track the sizes of `db` and `eb`
// from the original implementation.
//
//nolint:funlen // Forward/backward match accounting is one invariant-heavy binarydist scan.
func DiffBytes(obuf, nbuf []byte) int {
	if len(nbuf) < len(obuf) {
		obuf, nbuf = nbuf, obuf
	}
	var lenf int
	suffixArray := qsufsort(obuf)
	var dblen, eblen int

	// Compute the differences, writing ctrl as we go
	var scan, pos, length int
	var lastscan, lastpos, lastoffset int

	for scan < len(nbuf) {
		var oldscore int

		scan += length
		for scsc := scan; scan < len(nbuf); scan++ {
			pos, length = search(suffixArray, obuf, nbuf[scan:], 0, len(obuf))

			for ; scsc < scan+length; scsc++ {
				if scsc+lastoffset < len(obuf) &&
					obuf[scsc+lastoffset] == nbuf[scsc] {
					oldscore++
				}
			}

			if (length == oldscore && length != 0) || length > oldscore+8 {
				break
			}

			if scan+lastoffset < len(obuf) && obuf[scan+lastoffset] == nbuf[scan] {
				oldscore--
			}
		}

		if length != oldscore || scan == len(nbuf) {
			var score, bestForwardScore int
			lenf = 0

			for forwardLength := 0; lastscan+forwardLength < scan && lastpos+forwardLength < len(obuf); {
				if obuf[lastpos+forwardLength] == nbuf[lastscan+forwardLength] {
					score++
				}

				forwardLength++
				if score*2-forwardLength > bestForwardScore*2-lenf {
					bestForwardScore = score
					lenf = forwardLength
				}
			}

			lenb := 0

			if scan < len(nbuf) {
				var score, bestBackwardScore int

				for backwardLength := 1; (scan >= lastscan+backwardLength) && (pos >= backwardLength); backwardLength++ {
					if obuf[pos-backwardLength] == nbuf[scan-backwardLength] {
						score++
					}

					if score*2-backwardLength > bestBackwardScore*2-lenb {
						bestBackwardScore = score
						lenb = backwardLength
					}
				}
			}

			if lastscan+lenf > scan-lenb {
				overlap := (lastscan + lenf) - (scan - lenb)
				score := 0
				bestOverlapScore := 0
				lens := 0

				for overlapIndex := range overlap {
					if nbuf[lastscan+lenf-overlap+overlapIndex] == obuf[lastpos+lenf-overlap+overlapIndex] {
						score++
					}

					if nbuf[scan-lenb+overlapIndex] == obuf[pos-lenb+overlapIndex] {
						score--
					}

					if score > bestOverlapScore {
						bestOverlapScore = score
						lens = overlapIndex + 1
					}
				}

				lenf += lens - overlap
				lenb -= lens
			}

			var nonzero int

			for i := range lenf {
				if nbuf[lastscan+i]-obuf[lastpos+i] != 0 {
					nonzero++
				}
			}

			dblen += nonzero
			eblen += (scan - lenb) - (lastscan + lenf)
			lastscan = scan - lenb
			lastpos = pos - lenb
			lastoffset = pos - scan
		}
	}

	return dblen + eblen
}
