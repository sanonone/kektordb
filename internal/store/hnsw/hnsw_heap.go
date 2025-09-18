package hnsw

import "container/heap"

// è un elemento nella coda di priorità
type candidate struct {
	id       uint32
	distance float64
}

// è un mini heap di candidati, ovvero (i più vicini) in cima
// qui ci saranno i nodi ancora da visitare, il primo sarà
// sempre quello più vicino (distanza più piccola)
type minHeap []candidate

// ritorna dimensione heap
func (h minHeap) Len() int { return len(h) }

// ritorna true se i < j, per minheap minore vuol dire che i ha distanza più piccola di j
func (h minHeap) Less(i, j int) bool { return h[i].distance < h[j].distance }

// scambia elementi
func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

// aggiunge un elemento, ha il puntatore perché deve modificare la slice
func (h *minHeap) Push(x any) { *h = append(*h, x.(candidate)) }

// rimuove l'ultimo elemento dalla slice e lo restituisce
func (h *minHeap) Pop() any {
	// prima sposta l'elemento da rimuovere alla fine
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// è un max heap di candidati, ovvero (i più lontani) in cima
// qui ci saranno i k nodi già visitati che risultano migliori
// l'ordinamento è opposto a min heap quindi il primo elemento
// rappresenta il peggiore (per essere pronto a farsi buttare fuori
// per far posto ad un nodo migliore)
type maxHeap []candidate

func (h maxHeap) Len() int { return len(h) }

// ritorna true se i > j, per maxheap maggiore vuol dire che i ha una distanza maggiore di j
func (h maxHeap) Less(i, j int) bool { return h[i].distance > h[j].distance } // distanza più grande = priorità maggiore quindi deve salire in cima
func (h maxHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *maxHeap) Push(x any)        { *h = append(*h, x.(candidate)) }
func (h *maxHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// funzioni di convenienza per usare l'heap
func newMinHeap() *minHeap {
	h := &minHeap{}
	/*
		Prende una slice disordinata e la riorganizza per soddisfare la proprietà di heap
		Anche se inizia con una slice vuota, è una buona pratica chiamarlo per garantire che tutto sia inizializzato correttamente
	*/
	heap.Init(h)
	return h
}

func newMaxHeap() *maxHeap {
	h := &maxHeap{}
	heap.Init(h)
	return h
}
