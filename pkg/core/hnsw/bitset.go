package hnsw

type BitSet struct {
	buckets []uint64
}

func NewBitSet(initialCapacity uint32) *BitSet {
	numBuckets := (initialCapacity >> 6) + 1 // >> 6 == / 64
	return &BitSet{
		buckets: make([]uint64, numBuckets),
	}
}

func (bs *BitSet) grow(n uint32) {
	neededBuckets := (n >> 6) + 1 // >> 6 == / 64
	if uint32(len(bs.buckets)) < neededBuckets {
		newBuckets := make([]uint64, neededBuckets)
		copy(newBuckets, bs.buckets)
		bs.buckets = newBuckets
	}
}

func (bs *BitSet) Add(n uint32) {
	bucketIndex := n >> 6 // >> 6 == / 64

	if bucketIndex >= uint32(len(bs.buckets)) {
		bs.grow(n)
	}

	// Use bitwise AND: n & 63 == n % 64
	bs.buckets[bucketIndex] |= (1 << (n & 63))
}

func (bs *BitSet) Has(n uint32) bool {
	bucketIndex := n >> 6 // Equivalent to n / 64, but faster

	if bucketIndex >= uint32(len(bs.buckets)) {
		return false
	}
	// Use bitwise AND instead of modulo: n & 63 == n % 64
	return (bs.buckets[bucketIndex] & (1 << (n & 63))) != 0
}

func (bs *BitSet) Clear() {
	for i := range bs.buckets {
		bs.buckets[i] = 0
	}
}

func (bs *BitSet) EnsureCapacity(maxVal uint32) {
	neededBuckets := (maxVal >> 6) + 1 // >> 6 == / 64

	if uint32(len(bs.buckets)) < neededBuckets {
		bs.grow(maxVal)
	}
}
