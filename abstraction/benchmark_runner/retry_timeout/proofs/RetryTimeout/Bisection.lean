/-
Source: abstraction/benchmark_runner/retry_timeout/design.md (retry_timeout v0.3.0)

Theorem 2 of the Convergence section: the grid scan and the `refine` bisection of `feedback_points`, in exact
arithmetic. `g r = f r - r` and `above r ↔ 0 < g r`.
-/
import Mathlib.Algebra.Order.Archimedean.Real.Basic
import Mathlib.Topology.Instances.Real.Lemmas
import Mathlib.Topology.Order.IntermediateValue

namespace RetryTimeout

open Set

/-- `above r` of the design, for `g r = f r - r`. -/
noncomputable def above (g : ℝ → ℝ) (r : ℝ) : Bool := decide (0 < g r)

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- `refine` of the design: `n` bisection steps that keep `above lo = side`. -/
noncomputable def refine (p : ℝ → Bool) (side : Bool) : ℕ → ℝ × ℝ → ℝ × ℝ
  | 0, b => b
  | n + 1, b =>
    let mid := (b.1 + b.2) / 2
    refine p side n (if p mid = side then (mid, b.2) else (b.1, mid))

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- Theorem 2, invariant: `refine` keeps `p lo = side ≠ p hi`, stays inside the start bracket and halves its width
at every step. -/
theorem refine_invariant (p : ℝ → Bool) (side : Bool) (n : ℕ) :
    ∀ lo hi : ℝ, lo ≤ hi → p lo = side → p hi ≠ side →
      let b := refine p side n (lo, hi)
      lo ≤ b.1 ∧ b.1 ≤ b.2 ∧ b.2 ≤ hi ∧ p b.1 = side ∧ p b.2 ≠ side ∧ b.2 - b.1 = (hi - lo) / 2 ^ n := by
  induction n with
  | zero =>
    intro lo hi hle hlo hhi
    simp [refine, hle, hlo, hhi]
  | succ n ih =>
    intro lo hi hle hlo hhi
    simp only [refine]
    split_ifs with hm
    · obtain ⟨a1, a2, a3, a4, a5, a6⟩ := ih ((lo + hi) / 2) hi (by linarith) hm hhi
      refine ⟨by linarith, a2, a3, a4, a5, ?_⟩
      rw [a6, pow_succ]; field_simp; ring
    · obtain ⟨a1, a2, a3, a4, a5, a6⟩ := ih lo ((lo + hi) / 2) (by linarith) hlo hm
      refine ⟨a1, a2, by linarith, a4, a5, ?_⟩
      rw [a6, pow_succ]; field_simp; ring

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- Theorem 2, soundness: when `above` differs at the ends of a bracket where `g` is continuous, the bracket
returned by `refine` after `n` steps contains a zero `z` of `g` (a fixed point of RetryFeedback), so the returned
`hi` is within `(hi₀ - lo₀) / 2ⁿ` of it. -/
theorem refine_sound {g : ℝ → ℝ} {lo hi : ℝ} (hle : lo ≤ hi) (hc : ContinuousOn g (Icc lo hi))
    (hdiff : above g hi ≠ above g lo) (n : ℕ) :
    let b := refine (above g) (above g lo) n (lo, hi)
    ∃ z ∈ Icc b.1 b.2, g z = 0 ∧ |b.2 - z| ≤ (hi - lo) / 2 ^ n := by
  intro b
  obtain ⟨h1, h2, h3, h4, h5, h6⟩ := refine_invariant (above g) (above g lo) n lo hi hle rfl hdiff
  have hcb : ContinuousOn g (uIcc b.1 b.2) := by
    rw [uIcc_of_le h2]; exact hc.mono (Icc_subset_Icc h1 h3)
  have h0 : (0 : ℝ) ∈ uIcc (g b.1) (g b.2) := by
    have hne : above g b.2 ≠ above g b.1 := by rw [h4]; exact h5
    simp only [above, ne_eq, decide_eq_decide] at hne
    rcases le_or_gt (g b.1) 0 with ha | ha
    · have hb : 0 < g b.2 := by by_contra hb; exact hne ⟨fun h => absurd h hb, fun h => absurd h (not_lt.2 ha)⟩
      exact mem_uIcc.2 (Or.inl ⟨ha, hb.le⟩)
    · have hb : g b.2 ≤ 0 := by by_contra hb; push Not at hb; exact hne ⟨fun _ => ha, fun _ => hb⟩
      exact mem_uIcc.2 (Or.inr ⟨hb, ha.le⟩)
  obtain ⟨z, hz, hz0⟩ := intermediate_value_uIcc hcb h0
  rw [uIcc_of_le h2] at hz
  refine ⟨z, hz, hz0, ?_⟩
  rw [abs_of_nonneg (by linarith [hz.2])]
  linarith [hz.1]

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- Theorem 2, grid scan: if grid point `j > 0` is the first with `¬ above`, every earlier grid point has
`above`, and the cell `[x (j-1), x j]` holds a zero of `g`; `refine` on that cell then applies. -/
theorem scan_first_cell {g : ℝ → ℝ} {x : ℕ → ℝ} (hx : Monotone x) {j : ℕ} (hj : 0 < j)
    (hfirst : ∀ i < j, above g (x i) = true) (hnot : above g (x j) = false)
    (hc : ContinuousOn g (Icc (x (j - 1)) (x j))) :
    x (j - 1) ≤ x j ∧ above g (x j) ≠ above g (x (j - 1)) ∧ ∃ z ∈ Icc (x (j - 1)) (x j), g z = 0 := by
  have hle : x (j - 1) ≤ x j := hx (Nat.sub_le j 1)
  have hprev := hfirst (j - 1) (Nat.sub_lt hj one_pos)
  have hdiff : above g (x j) ≠ above g (x (j - 1)) := by rw [hprev, hnot]; decide
  obtain ⟨z, hz, hz0, -⟩ := refine_sound hle hc hdiff 0
  simp only [refine] at hz
  exact ⟨hle, hdiff, z, hz, hz0⟩

end RetryTimeout
