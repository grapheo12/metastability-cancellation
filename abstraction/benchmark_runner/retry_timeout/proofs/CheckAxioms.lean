/-
Source: abstraction/benchmark_runner/retry_timeout/design.md (retry_timeout v0.3.0)

Axiom check, not part of the library: `lake env lean CheckAxioms.lean` must print only
`[propext, Classical.choice, Quot.sound]` for every theorem.
-/
import RetryTimeout
open RetryTimeout
#print axioms exists_fixed_between
#print axioms iter_mono_bdd
#print axioms iter_tendsto_least_fixed
#print axioms stable_iff_exists_le
#print axioms fixed_exists_of_two_rho_le_one
#print axioms iter_exceeds_one
#print axioms basin_down
#print axioms refine_invariant
#print axioms refine_sound
#print axioms scan_first_cell
#print axioms eta_target_mono
#print axioms F_mono_of_parts
#print axioms stab_antitone
#print axioms stabGrid_antitone
#print axioms stabGrid_imp_stab
#print axioms etaBisect_brackets
#print axioms maxStableEta_grid_le
