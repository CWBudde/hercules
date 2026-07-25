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

func split(indexes, values []int, start, length, offset int) {
	if length < 16 {
		splitSmall(indexes, values, start, length, offset)
		return
	}

	pivotValue := values[indexes[start+length/2]+offset]
	lowerBound, upperBound := splitBounds(indexes, values, start, length, offset, pivotValue)
	partitionSuffixes(indexes, values, start, lowerBound, upperBound, offset, pivotValue)

	if lowerBound > start {
		split(indexes, values, start, lowerBound-start, offset)
	}

	for currentIndex := range upperBound - lowerBound {
		values[indexes[lowerBound+currentIndex]] = upperBound - 1
	}

	if lowerBound == upperBound-1 {
		indexes[lowerBound] = -1
	}

	if start+length > upperBound {
		split(indexes, values, upperBound, start+length-upperBound, offset)
	}
}

func splitSmall(indexes, values []int, start, length, offset int) {
	for groupIndex := start; groupIndex < start+length; {
		equalCount := selectSmallestSuffixes(indexes, values, groupIndex, start+length, offset)
		for currentIndex := range equalCount {
			values[indexes[groupIndex+currentIndex]] = groupIndex + equalCount - 1
		}

		if equalCount == 1 {
			indexes[groupIndex] = -1
		}

		groupIndex += equalCount
	}
}

func selectSmallestSuffixes(indexes, values []int, groupIndex, end, offset int) int {
	equalCount := 1
	pivotValue := values[indexes[groupIndex]+offset]

	for currentIndex := 1; groupIndex+currentIndex < end; currentIndex++ {
		value := values[indexes[groupIndex+currentIndex]+offset]
		if value < pivotValue {
			pivotValue = value
			equalCount = 0
		}

		if value == pivotValue {
			swap(indexes, groupIndex+currentIndex, groupIndex+equalCount)
			equalCount++
		}
	}

	return equalCount
}

func splitBounds(
	indexes, values []int,
	start, length, offset, pivotValue int,
) (int, int) {
	lowerCount, equalCount := 0, 0

	for currentIndex := start; currentIndex < start+length; currentIndex++ {
		value := values[indexes[currentIndex]+offset]
		if value < pivotValue {
			lowerCount++
		}

		if value == pivotValue {
			equalCount++
		}
	}

	lowerBound := start + lowerCount

	return lowerBound, lowerBound + equalCount
}

func partitionSuffixes(
	indexes, values []int,
	start, lowerBound, upperBound, offset, pivotValue int,
) {
	currentIndex, equalCount, greaterCount := start, 0, 0
	for currentIndex < lowerBound {
		switch value := values[indexes[currentIndex]+offset]; {
		case value < pivotValue:
			currentIndex++
		case value == pivotValue:
			swap(indexes, currentIndex, lowerBound+equalCount)
			equalCount++
		default:
			swap(indexes, currentIndex, upperBound+greaterCount)
			greaterCount++
		}
	}

	for lowerBound+equalCount < upperBound {
		if values[indexes[lowerBound+equalCount]+offset] == pivotValue {
			equalCount++
		} else {
			swap(indexes, lowerBound+equalCount, upperBound+greaterCount)
			greaterCount++
		}
	}
}

func qsufsort(obuf []byte) []int {
	suffixArray, ranks := initialSuffixArray(obuf)
	for offset := 1; suffixArray[0] != -(len(obuf) + 1); offset += offset {
		refineSuffixArray(suffixArray, ranks, offset)
	}

	for index := range len(obuf) + 1 {
		suffixArray[ranks[index]] = index
	}

	return suffixArray
}

func initialSuffixArray(obuf []byte) ([]int, []int) {
	var buckets [256]int
	suffixArray := make([]int, len(obuf)+1)
	ranks := make([]int, len(obuf)+1)

	for _, value := range obuf {
		buckets[value]++
	}

	for bucketIndex := 1; bucketIndex < len(buckets); bucketIndex++ {
		buckets[bucketIndex] += buckets[bucketIndex-1]
	}

	copy(buckets[1:], buckets[:])

	buckets[0] = 0
	for index, value := range obuf {
		buckets[value]++
		suffixArray[buckets[value]] = index
	}

	suffixArray[0] = len(obuf)
	for index, value := range obuf {
		ranks[index] = buckets[value]
	}

	for bucketIndex := 1; bucketIndex < len(buckets); bucketIndex++ {
		if buckets[bucketIndex] == buckets[bucketIndex-1]+1 {
			suffixArray[buckets[bucketIndex]] = -1
		}
	}

	suffixArray[0] = -1

	return suffixArray, ranks
}

func refineSuffixArray(suffixArray, ranks []int, offset int) {
	groupLength := 0

	for index := 0; index < len(suffixArray); {
		if suffixArray[index] < 0 {
			groupLength -= suffixArray[index]
			index -= suffixArray[index]

			continue
		}

		if groupLength != 0 {
			suffixArray[index-groupLength] = -groupLength
		}

		groupLength = ranks[suffixArray[index]] + 1 - index
		split(suffixArray, ranks, index, groupLength, offset)
		index += groupLength
		groupLength = 0
	}

	if groupLength != 0 {
		suffixArray[len(suffixArray)-groupLength] = -groupLength
	}
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
func DiffBytes(obuf, nbuf []byte) int {
	if len(nbuf) < len(obuf) {
		obuf, nbuf = nbuf, obuf
	}

	suffixArray := qsufsort(obuf)
	var dblen, eblen int

	// Compute the differences, writing ctrl as we go
	var scan, pos, length int
	var lastscan, lastpos, lastoffset int

	for scan < len(nbuf) {
		var oldscore int
		scan, pos, length, oldscore = findNextBinaryMatch(
			suffixArray, obuf, nbuf, scan, length, lastoffset,
		)

		if length != oldscore || scan == len(nbuf) {
			lenf := binaryForwardMatchLength(obuf, nbuf, lastscan, lastpos, scan)
			lenb := binaryBackwardMatchLength(obuf, nbuf, lastscan, scan, pos)
			lenf, lenb = adjustBinaryMatchOverlap(
				obuf, nbuf, lastscan, lastpos, scan, pos, lenf, lenb,
			)

			dblen += countBinaryDifferences(obuf, nbuf, lastscan, lastpos, lenf)
			eblen += (scan - lenb) - (lastscan + lenf)
			lastscan = scan - lenb
			lastpos = pos - lenb
			lastoffset = pos - scan
		}
	}

	return dblen + eblen
}

func findNextBinaryMatch(
	suffixArray []int,
	obuf, nbuf []byte,
	scan, length, lastoffset int,
) (int, int, int, int) {
	oldscore := 0
	scan += length
	position := 0

	for scanStart := scan; scan < len(nbuf); scan++ {
		var matchLength int

		position, matchLength = search(suffixArray, obuf, nbuf[scan:], 0, len(obuf))
		for ; scanStart < scan+matchLength; scanStart++ {
			if scanStart+lastoffset < len(obuf) &&
				obuf[scanStart+lastoffset] == nbuf[scanStart] {
				oldscore++
			}
		}

		if (matchLength == oldscore && matchLength != 0) || matchLength > oldscore+8 {
			return scan, position, matchLength, oldscore
		}

		if scan+lastoffset < len(obuf) && obuf[scan+lastoffset] == nbuf[scan] {
			oldscore--
		}

		length = matchLength
	}

	return scan, position, length, oldscore
}

func binaryForwardMatchLength(obuf, nbuf []byte, lastscan, lastpos, scan int) int {
	score, bestScore, bestLength := 0, 0, 0

	for length := 0; lastscan+length < scan && lastpos+length < len(obuf); {
		if obuf[lastpos+length] == nbuf[lastscan+length] {
			score++
		}

		length++
		if score*2-length > bestScore*2-bestLength {
			bestScore = score
			bestLength = length
		}
	}

	return bestLength
}

func binaryBackwardMatchLength(obuf, nbuf []byte, lastscan, scan, pos int) int {
	if scan >= len(nbuf) {
		return 0
	}

	score, bestScore, bestLength := 0, 0, 0

	for length := 1; scan >= lastscan+length && pos >= length; length++ {
		if obuf[pos-length] == nbuf[scan-length] {
			score++
		}

		if score*2-length > bestScore*2-bestLength {
			bestScore = score
			bestLength = length
		}
	}

	return bestLength
}

func adjustBinaryMatchOverlap(
	obuf, nbuf []byte,
	lastscan, lastpos, scan, pos, forwardLength, backwardLength int,
) (int, int) {
	if lastscan+forwardLength <= scan-backwardLength {
		return forwardLength, backwardLength
	}

	overlap := lastscan + forwardLength - (scan - backwardLength)
	score, bestScore, bestLength := 0, 0, 0

	for overlapIndex := range overlap {
		if nbuf[lastscan+forwardLength-overlap+overlapIndex] ==
			obuf[lastpos+forwardLength-overlap+overlapIndex] {
			score++
		}

		if nbuf[scan-backwardLength+overlapIndex] ==
			obuf[pos-backwardLength+overlapIndex] {
			score--
		}

		if score > bestScore {
			bestScore = score
			bestLength = overlapIndex + 1
		}
	}

	return forwardLength + bestLength - overlap, backwardLength - bestLength
}

func countBinaryDifferences(obuf, nbuf []byte, lastscan, lastpos, length int) int {
	nonzero := 0

	for index := range length {
		if nbuf[lastscan+index]-obuf[lastpos+index] != 0 {
			nonzero++
		}
	}

	return nonzero
}
