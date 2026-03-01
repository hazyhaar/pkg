╔════════════════════════════════════════════════════════════════════════╗
║  chunk -- Split text into overlapping chunks for RAG / FTS5 indexing  ║
╠════════════════════════════════════════════════════════════════════════╣
║  Module: github.com/hazyhaar/pkg/chunk                               ║
║  Files:  chunk.go                                                    ║
║  Deps:   stdlib only (strings, unicode, unicode/utf8)                ║
╚════════════════════════════════════════════════════════════════════════╝

PIPELINE
========

                   ┌─────────────────────────────────────────────┐
  raw text ──────> │              Split(text, opts)              │
  (string)         │                                             │
                   │  1. tokenize(text) -> []string (whitespace) │
                   │  2. if len(words) <= MaxTokens -> 1 chunk   │
                   │  3. else: splitParagraphAware()             │
                   │     a. split on "\n\n" boundaries           │
                   │     b. accumulate paragraphs until MaxTokens│
                   │     c. oversized paragraphs -> slidingWindow│
                   │     d. overlap extracted from chunk tail     │
                   │  4. fallback: slidingWindow() if 1 paragraph│
                   └──────────────────┬──────────────────────────┘
                                      │
                                      v
                              ┌──────────────┐
                              │  []Chunk      │
                              │  .Index  int  │
                              │  .Text   str  │
                              │  .TokenCount  │
                              │  .OverlapPrev │
                              └──────────────┘

SPLITTING STRATEGY (priority order)
====================================

  Input text
      │
      v
  ┌──────────────────────┐
  │ Split on \n\n        │  <-- paragraph boundaries
  │ (splitOnDoubleLF)    │
  └──────────┬───────────┘
             │
     ┌───────┴───────┐
     │ para <= max?  │
     │  YES: buffer  │──> flush when buffer > MaxTokens
     │  NO: sliding  │──> slidingWindow(paraWords, opts)
     └───────────────┘
             │
             v
  ┌──────────────────────┐
  │ slidingWindow        │  <-- word-level sliding window
  │ stride = max-overlap │
  │ merge if < min       │
  └──────────────────────┘

OVERLAP MECHANISM
=================

  Chunk N-1:  [... word_a word_b word_c word_d]
                              └─────overlap──────┐
  Chunk N:    [word_c word_d word_e word_f ...]   │
              OverlapPrev = 2 ◄──────────────────┘

  extractOverlap(text, n) -> last n words of text
  computeOverlap(a, b)    -> count leading words of b matching trailing of a

EXPORTED TYPES
==============

  Options {
      MaxTokens      int   // max tokens per chunk          (default 512)
      OverlapTokens  int   // overlap between chunks        (default 64)
      MinChunkTokens int   // chunks smaller -> merge prev  (default 32)
  }

  Chunk {
      Index       int    // 0-based position
      Text        string // chunk content
      TokenCount  int    // approx token count (word count)
      OverlapPrev int    // tokens overlapping with previous
  }

EXPORTED FUNCTIONS
==================

  Split(text string, opts Options) []Chunk
      Main entry point. Returns nil for empty text.

  CountTokens(text string) int
      Whitespace-split word count. Used internally for chunking.

  EstimateTokens(text string) int
      BPE-approximation: avg of (runeCount/4) and (wordCount*4/3).
      For external callers estimating LLM token usage.

TOKEN COUNTING NOTE
====================

  CountTokens  = len(strings.Fields(text))     -- exact word count
  EstimateTokens = (chars/4 + words*1.33) / 2  -- BPE heuristic

  Split() uses CountTokens internally (word = token).
  EstimateTokens is for external callers needing LLM-style estimates.

DATA FORMAT
===========

  Input:  plain text string (any encoding, whitespace-separated words)
  Output: []Chunk -- ordered, 0-indexed, overlap metadata attached

  Guarantees:
    - Each chunk.TokenCount <= opts.MaxTokens
    - chunks[0].OverlapPrev == 0
    - chunks[i].Index == i
    - No empty chunks in output
    - Split("") returns nil
    - Chunks below MinChunkTokens are merged with predecessor

NO DATABASE, NO HTTP, NO MIDDLEWARE
