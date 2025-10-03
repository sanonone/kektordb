package hnsw

type Bitset []uint64

// NewBitset crea un nuovo bitset in grado di contenere 'size' elementi.
func NewBitset(size uint32) Bitset {
	// Calcola quanti uint64 ci servono.
	// (size + 63) / 64 è un modo veloce per fare il 'ceil' della divisione.
	return make(Bitset, (size+63)>>6)
}

// Set imposta il bit per l'id a 'true'.
func (b Bitset) Set(id uint32) {
	// id >> 6 è come id / 64
	// id & 63 è come id % 64
	b[id>>6] |= (1 << (id & 63))
}

// IsSet controlla se il bit per l'id è 'true'.
func (b Bitset) IsSet(id uint32) bool {
	return (b[id>>6] & (1 << (id & 63))) != 0
}

// Clear resetta tutti i bit a 'false'.
func (b Bitset) Clear() {
	for i := range b {
		b[i] = 0
	}
}
