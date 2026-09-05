# Examples: demo-golden

`docs/` is a small Japanese knowledge base for a fictional EC shop's
support site (returns, shipping, payment, account, warranty, products,
contact, FAQ). `golden-dataset.json` has 50 Japanese questions, each with
the filename(s) that should be retrieved to answer it — a Golden Dataset
for Retrieval evaluation (docs/ROADMAP.md W7).

```sh
./dist/forgeai serve &

# Ingest the sample docs into a "demo" knowledge base.
./dist/forgeai ingest ./examples/docs -kb demo

# Import the golden dataset, scoped to that knowledge base.
./dist/forgeai eval import -kb demo demo-golden ./examples/golden-dataset.json

# Run it: scores Hybrid Search against each question's expected filename(s)
# and prints Recall@K / Precision@K / MRR / Hit Rate.
./dist/forgeai eval run demo-golden
```
