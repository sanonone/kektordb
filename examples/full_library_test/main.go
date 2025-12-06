package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/sanonone/kektordb/pkg/engine"
)

const DataDir = "./test_data"

func main() {
	// Clean up previous runs
	os.RemoveAll(DataDir)

	fmt.Println("STARTING KEKTORDB LIBRARY TEST SUITE")
	fmt.Println("------------------------------------------------")

	// ==========================================
	// 1. TEST LIFECYCLE & CONFIG
	// ==========================================
	fmt.Println("[1] Engine Initialization...")
	opts := engine.DefaultOptions(DataDir)
	opts.AutoSaveInterval = 1 * time.Second // Fast autosave for testing purposes
	opts.AutoSaveThreshold = 1

	db, err := engine.Open(opts)
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}
	fmt.Println("OK: Engine opened.")

	// ==========================================
	// 2. TEST KEY-VALUE STORE
	// ==========================================
	fmt.Println("\n[2] Testing Key-Value Store...")

	// SET
	if err := db.KVSet("config_key", []byte("my_config_value")); err != nil {
		log.Fatalf("KVSet failed: %v", err)
	}

	// GET
	val, found := db.KVGet("config_key")
	if !found || string(val) != "my_config_value" {
		log.Fatalf("KVGet failed or incorrect value: %s", string(val))
	}

	// DELETE
	if err := db.KVDelete("config_key"); err != nil {
		log.Fatalf("KVDelete failed: %v", err)
	}
	_, found = db.KVGet("config_key")
	if found {
		log.Fatalf("Key should have been deleted!")
	}
	fmt.Println("OK: KV Store (Set/Get/Delete).")

	// ==========================================
	// 3. TEST VECTOR INDEX & SINGLE CRUD
	// ==========================================
	fmt.Println("\n[3] Testing Vector CRUD (Single)...")
	idxName := "test_crud"

	// VCreate
	err = db.VCreate(idxName, distance.Cosine, 16, 200, distance.Float32, "english", nil)
	if err != nil {
		log.Fatalf("VCreate failed: %v", err)
	}

	// VAdd
	vec1 := []float32{0.1, 0.2, 0.3}
	meta1 := map[string]any{"category": "A", "desc": "red apple"}
	if err := db.VAdd(idxName, "doc1", vec1, meta1); err != nil {
		log.Fatalf("VAdd failed: %v", err)
	}

	// VGet
	data, err := db.VGet(idxName, "doc1")
	if err != nil {
		log.Fatalf("VGet failed: %v", err)
	}
	if data.Metadata["category"] != "A" {
		log.Fatalf("Metadata corrupted in VGet")
	}

	// VDelete
	if err := db.VDelete(idxName, "doc1"); err != nil {
		log.Fatalf("VDelete failed: %v", err)
	}

	// Verify Delete (Soft delete: VSearch must not find it)
	res, _ := db.VSearch(idxName, vec1, 1, "", 100, 0.5)
	if len(res) > 0 {
		log.Fatalf("Deleted vector found in search results!")
	}
	fmt.Println("OK: Vector CRUD (Create/Add/Get/Delete).")

	// ==========================================
	// 4. TEST BATCH & IMPORT
	// ==========================================
	fmt.Println("\n[4] Testing Batch & Import...")

	// VAddBatch
	batchItems := []types.BatchObject{
		{Id: "b1", Vector: []float32{1.0, 0.0, 0.0}, Metadata: map[string]any{"tag": "batch"}},
		{Id: "b2", Vector: []float32{0.0, 1.0, 0.0}, Metadata: map[string]any{"tag": "batch"}},
	}
	if err := db.VAddBatch(idxName, batchItems); err != nil {
		log.Fatalf("VAddBatch failed: %v", err)
	}

	// VGetMany
	items, err := db.VGetMany(idxName, []string{"b1", "b2", "missing"})
	if err != nil || len(items) != 2 {
		log.Fatalf("VGetMany failed or incorrect count: %d found", len(items))
	}

	// VImport (Creates new index, bypasses AOF, forces snapshot)
	importIdx := "test_import"
	db.VCreate(importIdx, distance.Euclidean, 16, 200, distance.Float32, "", nil)

	importItems := []types.BatchObject{
		{Id: "i1", Vector: []float32{0.5, 0.5, 0.5}},
		{Id: "i2", Vector: []float32{0.1, 0.1, 0.1}},
	}

	if err := db.VImport(importIdx, importItems); err != nil {
		log.Fatalf("VImport failed: %v", err)
	}

	// Immediate verification
	resImp, _ := db.VSearch(importIdx, []float32{0.5, 0.5, 0.5}, 1, "", 100, 0.5)
	if len(resImp) == 0 || resImp[0] != "i1" {
		log.Fatalf("VImport data not found after snapshot.")
	}

	fmt.Println("OK: Batch Add, GetMany, and VImport.")

	// ==========================================
	// 5. TEST HYBRID SEARCH & FILTERING
	// ==========================================
	fmt.Println("\n[5] Testing Search Capabilities...")

	// Prepare data for filters
	db.VAdd(idxName, "s1", []float32{0.9, 0.9, 0.9}, map[string]any{"price": 10.0, "desc": "cheap item"})
	db.VAdd(idxName, "s2", []float32{0.9, 0.9, 0.9}, map[string]any{"price": 100.0, "desc": "expensive luxury item"})

	// Test 1: Numeric Filter
	resFilter, _ := db.VSearch(idxName, []float32{0.9, 0.9, 0.9}, 5, "price < 50", 100, 0.5)
	if len(resFilter) != 1 || resFilter[0] != "s1" {
		log.Fatalf("Numeric filter failed. Found: %v", resFilter)
	}

	// Test 2: Hybrid Text Search
	// Searching for "luxury". s2 should win even if the vector is identical to s1.
	resHybrid, _ := db.VSearch(idxName, []float32{0.9, 0.9, 0.9}, 5, "CONTAINS(desc, 'luxury')", 100, 0.5)
	if len(resHybrid) == 0 || resHybrid[0] != "s2" {
		log.Fatalf("Hybrid search failed. Top result: %v", resHybrid)
	}

	fmt.Println("OK: Filters and Hybrid Search.")

	// ==========================================
	// 6. TEST COMPRESSION (Int8)
	// ==========================================
	fmt.Println("\n[6] Testing Compression (Float32 -> Int8)...")

	int8Idx := "test_int8_dedicated"
	// Create a new index to isolate the test
	db.VCreate(int8Idx, distance.Cosine, 16, 200, distance.Float32, "", nil)

	// 1. Insert a specific "Target" (all high positive values)
	targetVec := make([]float32, 128)
	for i := range targetVec {
		targetVec[i] = rand.Float32() // [0, 1]
	}
	db.VAdd(int8Idx, "target", targetVec, nil)
	for i := 0; i < 100; i++ {
		noiseVec := make([]float32, 128)
		for j := range noiseVec {
			noiseVec[j] = rand.Float32()*2 - 1 // [-1, 1]
		}
		db.VAdd(int8Idx, fmt.Sprintf("noise_%d", i), noiseVec, nil)
	}

	fmt.Println("   -> Data inserted. Starting compression...")

	// 3. Compress
	if err := db.VCompress(int8Idx, distance.Int8); err != nil {
		log.Fatalf("VCompress failed: %v", err)
	}

	// 4. Verify Search (Must find "target")
	resComp, err := db.VSearch(int8Idx, targetVec, 1, "", 100, 0.5)

	if err != nil {
		log.Fatalf("Post-compression search error: %v", err)
	}

	if len(resComp) == 0 {
		log.Fatalf("No results found in compressed index!")
	}

	if resComp[0] != "target" {
		log.Fatalf("INT8 MATH FAILURE: Found '%s' instead of 'target'. Quantization destroyed data integrity.", resComp[0])
	}

	// 5. Verify Data Integrity (VGet must work and return reconstructed float32s)
	dataComp, err := db.VGet(int8Idx, "target")
	if err != nil {
		log.Fatalf("VGet on compressed index failed: %v", err)
	}

	// Check if reconstructed vector is "similar" to original
	diff := dataComp.Vector[0] - targetVec[0]
	if diff < -0.1 || diff > 0.1 {
		fmt.Printf("Warning: Significant Int8 precision loss: Orig %f -> Rec %f\n", targetVec[0], dataComp.Vector[0])
	}

	fmt.Println("OK: VCompress Int8 (Target found, data readable).")

	// ==========================================
	// 7. TEST COMPRESSION (Float16)
	// ==========================================
	fmt.Println("\n[7] Testing Compression (Float32 -> Float16)...")

	f16Idx := "test_f16_dedicated"
	// Create Euclidean index (standard for Float16)
	if err := db.VCreate(f16Idx, distance.Euclidean, 16, 200, distance.Float32, "", nil); err != nil {
		log.Fatalf("VCreate F16 failed: %v", err)
	}

	// 1. Insert Target (Numbers that aren't perfect powers of 2 to test precision)
	targetVecF16 := []float32{1.234, 5.678, 9.101}
	db.VAdd(f16Idx, "target_f16", targetVecF16, nil)

	// 2. Insert Noise
	for i := 0; i < 20; i++ {
		db.VAdd(f16Idx, fmt.Sprintf("noise_f16_%d", i), []float32{0.0, 0.0, 0.0}, nil)
	}

	fmt.Println("   -> Data inserted. Starting Float16 compression...")

	// 3. Compress to Float16
	if err := db.VCompress(f16Idx, distance.Float16); err != nil {
		log.Fatalf("VCompress Float16 failed: %v", err)
	}

	// 4. Verify Search
	resCompF16, err := db.VSearch(f16Idx, targetVecF16, 1, "", 100, 0.0)
	if err != nil {
		log.Fatalf("Search error F16: %v", err)
	}

	if len(resCompF16) == 0 || resCompF16[0] != "target_f16" {
		log.Fatalf("FLOAT16 FAILURE: Found '%v' instead of 'target_f16'", resCompF16)
	}

	// 5. Verify Data Integrity
	dataCompF16, err := db.VGet(f16Idx, "target_f16")
	if err != nil {
		log.Fatalf("VGet F16 failed: %v", err)
	}

	// Precision check: Diff should not exceed ~0.01 for small numbers
	origVal := targetVecF16[0] // 1.234
	recupVal := dataCompF16.Vector[0]
	diffF16 := origVal - recupVal

	if diffF16 < -0.01 || diffF16 > 0.01 {
		fmt.Printf("Warning: Excessive Float16 precision loss? Orig %f -> Rec %f\n", origVal, recupVal)
	} else {
		fmt.Printf("   -> Precision check OK: %f ~= %f\n", origVal, recupVal)
	}

	fmt.Println("OK: VCompress Float16.")

	// ==========================================
	// 8. TEST INDEX DELETION
	// ==========================================
	fmt.Println("\n[8] Testing Index Deletion...")
	if err := db.VDeleteIndex(idxName); err != nil {
		log.Fatalf("VDeleteIndex failed: %v", err)
	}

	_, err = db.VSearch(idxName, []float32{0, 0, 0}, 1, "", 100, 0)
	if err == nil {
		log.Fatalf("Index should have been deleted!")
	}
	fmt.Println("OK: VDeleteIndex.")

	// ==========================================
	// 9. TEST PERSISTENCE (Restart)
	// ==========================================
	fmt.Println("\n[9] Testing Persistence (Restart)...")

	// Close DB (flushes to disk)
	db.Close()
	fmt.Println("   -> DB Closed.")

	// Reopen
	db2, err := engine.Open(opts)
	if err != nil {
		log.Fatalf("Failed to reopen DB: %v", err)
	}
	defer db2.Close()

	// Verify "test_import" index still exists
	resPersist, _ := db2.VSearch(importIdx, []float32{0.5, 0.5, 0.5}, 1, "", 100, 0.5)
	if len(resPersist) == 0 || resPersist[0] != "i1" {
		log.Fatalf("Data lost after restart! Persistence failed.")
	}

	fmt.Println("OK: Persistence verified (Data survived restart).")

	db = db2
	defer db.Close()

	fmt.Println("\nALL TESTS PASSED SUCCESSFULLY!")

	// ==========================================
	// 10. TEST MAINTENANCE & OPTIMIZER
	// ==========================================
	fmt.Println("\n🔹 10. Testing Maintenance (Vacuum & Config)...")

	maintIdx := "test_maintenance"
	db.VCreate(maintIdx, distance.Cosine, 16, 200, distance.Float32, "", nil)

	// Aggiungiamo 3 nodi: A, B, C
	// A e B sono vicini, C è lontano.
	db.VAdd(maintIdx, "nodeA", []float32{1.0, 0.0, 0.0}, nil)
	db.VAdd(maintIdx, "nodeB", []float32{0.9, 0.1, 0.0}, nil)
	db.VAdd(maintIdx, "nodeC", []float32{0.0, 1.0, 0.0}, nil)

	// Cancelliamo nodeB. Ora nodeA potrebbe avere un link rotto verso B.
	db.VDelete(maintIdx, "nodeB")

	// 1. Aggiorniamo la configurazione a caldo
	// Abilitiamo il Refine e cambiamo intervalli
	newCfg := hnsw.AutoMaintenanceConfig{
		VacuumInterval:  hnsw.Duration(5 * time.Second),
		DeleteThreshold: 0.1,
		RefineEnabled:   true, // Abilitiamo Refine
		RefineInterval:  hnsw.Duration(2 * time.Second),
		RefineBatchSize: 10,
	}

	fmt.Println("   -> Updating Index Config...")
	if err := db.VUpdateIndexConfig(maintIdx, newCfg); err != nil {
		log.Fatalf("❌ VUpdateIndexConfig fallito: %v", err)
	}

	// 2. Trigger Manuale Vacuum
	// Questo dovrebbe trovare nodeB marcato 'Deleted', rimuovere i link da nodeA, e liberare memoria.
	fmt.Println("   -> Triggering Manual Vacuum...")
	if err := db.VTriggerMaintenance(maintIdx, "vacuum"); err != nil {
		log.Fatalf("❌ VTriggerMaintenance (vacuum) fallito: %v", err)
	}
	// Nota: Guarda i log del server per vedere "[Optimizer] Vacuum complete..."

	// 3. Trigger Manuale Refine
	fmt.Println("   -> Triggering Manual Refine...")
	if err := db.VTriggerMaintenance(maintIdx, "refine"); err != nil {
		log.Fatalf("❌ VTriggerMaintenance (refine) fallito: %v", err)
	}

	// Verifica che nodeA esista ancora e trovi nodeC (o nulla) ma non nodeB
	resMaint, _ := db.VSearch(maintIdx, []float32{1.0, 0.0, 0.0}, 5, "", 100, 0.5)
	for _, id := range resMaint {
		if id == "nodeB" {
			log.Fatalf("❌ ERRORE GRAVE: nodeB trovato dopo Vacuum!")
		}
	}

	fmt.Println("✅ Maintenance Ops OK (Config update, Triggers executed).")
}
