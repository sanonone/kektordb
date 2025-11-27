module full_test

go 1.25.4

require github.com/sanonone/kektordb v0.0.0

require (
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/tidwall/btree v1.8.1 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/sys v0.30.0 // indirect
	gonum.org/v1/gonum v0.16.0 // indirect
)

// Punta alla root del tuo progetto locale
replace github.com/sanonone/kektordb => ../../
