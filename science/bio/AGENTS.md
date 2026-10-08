<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# science/bio

Bounded FASTA parsing. DNA/RNA string helpers. Stdlib only. Stateless. No fx.
Own module: `github.com/golusoris/golusoris/science/bio`.

## API

```go
seqs, err := bio.ParseFASTA(r)
seqs, err = bio.ParseFASTAContext(ctx, r, bio.FASTAOptions{
    MaxBytes: 64 << 20, MaxRecords: 100_000, MaxLineBytes: 1 << 20,
})
rc := bio.ReverseComplement("ACGT")
gc := bio.GCContent("GCGC")
```

## Parser contract

- `ParseFASTA`: compatibility API. Finite exported defaults.
- `ParseFASTAContext`: cancellation plus explicit byte, record, line limits.
- Zero limit: default. Negative limit: `ErrInvalidFASTAOptions`.
- Exact limit: accepted. Limit plus one: typed limit error.
- Sequence lines: upper-case. Trailing CR: removed. Preamble: ignored.
- Generic reader cancellation: checked before and after each read.

## Migration

- Removed `DNASeq` and `RNASeq`. No repository caller used them.
- Before: `dna := bio.DNASeq("sample", "ACGT")`.
- After: `dna := linear.NewSeq("sample", []alphabet.Letter("ACGT"), alphabet.DNA)`
  with direct `biogo/alphabet` and `biogo/seq/linear` imports.
- RNA migration uses `alphabet.RNA` with the same `linear.NewSeq` call.
- `GCContent("")`: `0`. Reverse complement: unknown bases unchanged.
