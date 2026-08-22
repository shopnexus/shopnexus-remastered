-- Scaling a compiled predicate's weight by how common its value is bought a per-value tuning
-- knob for a ranking that is already three fused signals, and it needed a table-wide recount on
-- the sweeper plus a read on every search to move a weight in the third decimal. The per-attribute
-- weight is what the search keeps.
DROP TABLE IF EXISTS "signal_selectivity";
