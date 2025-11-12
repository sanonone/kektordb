package hnsw

type BitSet struct {
	buckets []uint64
}

func NewBitSet(initialCapacity uint32) *BitSet {
	numBuckets := (initialCapacity / 64) + 1
	return &BitSet{
		buckets: make([]uint64, numBuckets),
	}
}

func (bs *BitSet) grow(n uint32) {
	neededBuckets := (n / 64) + 1
	if uint32(len(bs.buckets)) < neededBuckets {
		newBuckets := make([]uint64, neededBuckets)
		copy(newBuckets, bs.buckets)
		bs.buckets = newBuckets
	}
}

func (bs *BitSet) Add(n uint32) {
	bucketIndex := n / 64

	needsGrow := bucketIndex >= uint32(len(bs.buckets))

	if needsGrow {
		if bucketIndex >= uint32(len(bs.buckets)) {
			bs.grow(n)
		}
	}

	bitIndex := n % 64
	bs.buckets[bucketIndex] |= (1 << bitIndex)
}

func (bs *BitSet) Has(n uint32) bool {
	bucketIndex := n / 64

	if bucketIndex >= uint32(len(bs.buckets)) {
		return false
	}
	bitIndex := n % 64
	return (bs.buckets[bucketIndex] & (1 << bitIndex)) != 0
}

func (bs *BitSet) Clear() {
	for i := range bs.buckets {
		bs.buckets[i] = 0
	}
}

func (bs *BitSet) EnsureCapacity(maxVal uint32) {
	neededBuckets := (maxVal / 64) + 1

	currentLen := uint32(len(bs.buckets))

	if currentLen < neededBuckets {
		if uint32(len(bs.buckets)) < neededBuckets {
			bs.grow(maxVal)
		}
	}
}
