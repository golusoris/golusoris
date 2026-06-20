# Agent guide — science/bio/

Bioinformatics helpers — sequence handling, FASTA parsing, and basic DNA/RNA
utilities over biogo. Stateless utility — **no fx wiring**. Own go.mod
sub-module; import directly: `github.com/golusoris/golusoris/science/bio`.

## API

```go
seqs, err := bio.ParseFASTA(r)          // []bio.Sequence{Name, Seq}
rc := bio.ReverseComplement("ACGT")     // -> "ACGT"
gc := bio.GCContent("GCGC")             // fraction 0–1

s := bio.DNASeq("name", "ACGT")         // *biogo linear.Seq (DNA)
s = bio.RNASeq("name", "ACGU")          // *biogo linear.Seq (RNA)
```

## Why biogo/biogo

Established Go bioinformatics toolkit; `DNASeq`/`RNASeq` hand back native
`linear.Seq` values for callers that need biogo's alignment/alphabet machinery.

## Notes

- Separate go.mod because biogo's dependency graph is large and specialised.
- `ParseFASTA` upper-cases sequence lines and trims CR; `ReverseComplement` /
  `GCContent` are pure-stdlib and don't touch biogo.
- `GCContent("")` returns 0; `ReverseComplement` passes through unknown bases.
