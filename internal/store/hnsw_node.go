package store

// rappresenta un singolo nodo nel grafo HNSW
type Node struct {
	id string 
	vector []float32

	// è una slice di slice, l'indice esterno rappresenta
	// il livello del grafo, l'indice interno è la lista 
	// dei vicini a quel livello. connections[0] sono i vicini al 
	// livello base
	connections [][]uint32 // uint32 per ID n0di per efficienza in memoria 
	deleted bool // flag per le soft deletes
}
