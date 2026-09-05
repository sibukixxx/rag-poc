# Examples: demo-golden

`docs/` is a small Japanese knowledge base for a fictional EC shop's
support site (returns, shipping, payment, account, warranty, products,
contact, FAQ). `golden-dataset.json` has 50 Japanese questions, each with
the filename(s) that should be retrieved to answer it and a short
reference answer — a Golden Dataset for Retrieval evaluation
(docs/ROADMAP.md W7) and LLM Judge evaluation (W8).

```sh
./dist/forgeai serve &

# Ingest the sample docs into a "demo" knowledge base.
./dist/forgeai ingest ./examples/docs -kb demo

# Import the golden dataset, scoped to that knowledge base.
./dist/forgeai eval import -kb demo demo-golden ./examples/golden-dataset.json

# Run it: scores Hybrid Search against each question's expected filename(s)
# and prints Recall@K / Precision@K / MRR / Hit Rate.
./dist/forgeai eval run demo-golden

# Judge run: also answers each question through the RAG pipeline and has the
# "judge" alias score Correctness / Groundedness / Relevance against the
# reference answer, then lists low-scoring cases with the judge's reason.
./dist/forgeai eval run -judge demo-golden
```
