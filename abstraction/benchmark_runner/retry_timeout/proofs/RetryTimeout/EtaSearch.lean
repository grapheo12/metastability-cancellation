/-
Source: abstraction/benchmark_runner/retry_timeout/design.md (retry_timeout v0.3.0)

Theorem 3 of the Convergence section: the MaxStableEta search, in exact arithmetic. `F η r` is RetryFeedback at
target load `η` (its RetryTimeout computed from the EtaSlack target), and `F η` is pointwise nondecreasing in `η`
(assumption H6 of the design; `F_mono_of_parts` derives it from the monotone pieces).
-/
import Mathlib.Algebra.Order.Archimedean.Real.Basic
import Mathlib.Order.ConditionallyCompleteLattice.Basic

namespace RetryTimeout

open Set

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- The EtaSlack target `max 0 (min 1 (η - ρ₀))` is nondecreasing in `η`. -/
theorem eta_target_mono (ρ₀ : ℝ) : Monotone fun η : ℝ => max 0 (min 1 (η - ρ₀)) := by
  intro a b hab
  dsimp only
  gcongr

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- H6 from its parts: the target is nondecreasing in `η`, the RetryTimeout is nonincreasing in the target (it
may take values in any preorder, e.g. `ℝ` with `+∞`), and the tail is nonincreasing in the timeout; then RetryFeedback
`ρ₀ (1 + tail (T (target η)) r)` is nondecreasing in `η` at every load `r`. -/
theorem F_mono_of_parts {β : Type*} [Preorder β] {ρ₀ : ℝ} (hρ : 0 ≤ ρ₀) {target : ℝ → ℝ}
    (htarget : Monotone target) {T : ℝ → β} (hT : Antitone T) {tail : β → ℝ → ℝ}
    (htail : ∀ r, Antitone fun t => tail t r) (r : ℝ) :
    Monotone fun η => ρ₀ * (1 + tail (T (target η)) r) := by
  intro a b hab
  have : tail (T (target a)) r ≤ tail (T (target b)) r := htail r (hT (htarget hab))
  dsimp only
  gcongr

/-- The exact stability test of MaxStableEta: RetryFeedback dips to the diagonal somewhere on `[ρ₀, 1]`
(equivalent to a fixed point there, by `stable_iff_exists_le`). -/
def Stab (F : ℝ → ℝ → ℝ) (ρ₀ η : ℝ) : Prop := ∃ r ∈ Icc ρ₀ 1, F η r ≤ r

/-- The grid test that `feedback_points` actually performs: some grid point `x i`, `i ≤ N`, inside `[ρ₀, 1]` is not
`above`. -/
def StabGrid (F : ℝ → ℝ → ℝ) (ρ₀ : ℝ) (x : ℕ → ℝ) (N : ℕ) (η : ℝ) : Prop :=
  ∃ i ≤ N, x i ∈ Icc ρ₀ 1 ∧ F η (x i) ≤ x i

section

variable {F : ℝ → ℝ → ℝ} {ρ₀ : ℝ} (hF : ∀ r, Monotone fun η => F η r)
include hF

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- Theorem 3(a): the stable `η` form a down-set (an interval starting at `ρ₀`). -/
theorem stab_antitone {η₁ η₂ : ℝ} (h : η₁ ≤ η₂) (hs : Stab F ρ₀ η₂) : Stab F ρ₀ η₁ := by
  obtain ⟨r, hr, hFr⟩ := hs
  exact ⟨r, hr, (hF r h).trans hFr⟩

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- Theorem 3(d): the grid test is also a down-set. -/
theorem stabGrid_antitone {x : ℕ → ℝ} {N : ℕ} {η₁ η₂ : ℝ} (h : η₁ ≤ η₂) (hs : StabGrid F ρ₀ x N η₂) :
    StabGrid F ρ₀ x N η₁ := by
  obtain ⟨i, hi, hx, hFx⟩ := hs
  exact ⟨i, hi, hx, (hF (x i) h).trans hFx⟩

end

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- Theorem 3(d): the grid test implies the exact one. -/
theorem stabGrid_imp_stab {F : ℝ → ℝ → ℝ} {ρ₀ : ℝ} {x : ℕ → ℝ} {N : ℕ} {η : ℝ}
    (hs : StabGrid F ρ₀ x N η) : Stab F ρ₀ η := by
  obtain ⟨i, -, hx, hFx⟩ := hs
  exact ⟨x i, hx, hFx⟩

/-- The bisection on `η` of `max_stable_eta`, for any test `P`. -/
noncomputable def etaBisect (P : ℝ → Prop) : ℕ → ℝ × ℝ → ℝ × ℝ
  | 0, b => b
  | n + 1, b =>
    let mid := (b.1 + b.2) / 2
    haveI := Classical.propDecidable (P mid)
    etaBisect P n (if P mid then (mid, b.2) else (b.1, mid))

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- Theorem 3(c): for a down-set test `P` with `P lo` and `¬ P hi`, after `n` steps the bracket keeps
`P lo_n`, `¬ P hi_n`, has width `(hi - lo) / 2ⁿ`, and contains `sSup {η | P η}` (the MaxStableEta). -/
theorem etaBisect_brackets {P : ℝ → Prop} (hdown : ∀ a b, a ≤ b → P b → P a) (n : ℕ) :
    ∀ lo hi : ℝ, lo ≤ hi → P lo → ¬ P hi →
      let b := etaBisect P n (lo, hi)
      P b.1 ∧ ¬ P b.2 ∧ b.2 - b.1 = (hi - lo) / 2 ^ n ∧ b.1 ≤ sSup {η | P η} ∧ sSup {η | P η} ≤ b.2 := by
  -- every stable η lies below any unstable one
  have hbelow : ∀ u, ¬ P u → ∀ η ∈ {η | P η}, η ≤ u := fun u hu η hη => by
    by_contra hlt; push Not at hlt; exact hu (hdown u η hlt.le hη)
  have hsup : ∀ l u, P l → ¬ P u → l ≤ sSup {η | P η} ∧ sSup {η | P η} ≤ u := fun l u hl hu =>
    ⟨le_csSup ⟨u, hbelow u hu⟩ hl, csSup_le ⟨l, hl⟩ (hbelow u hu)⟩
  induction n with
  | zero =>
    intro lo hi _ hlo hhi
    simp only [etaBisect, pow_zero, div_one]
    exact ⟨hlo, hhi, trivial, hsup lo hi hlo hhi⟩
  | succ n ih =>
    intro lo hi hle hlo hhi
    simp only [etaBisect]
    split_ifs with hm
    · obtain ⟨a1, a2, a3, a4⟩ := ih ((lo + hi) / 2) hi (by linarith) hm hhi
      refine ⟨a1, a2, ?_, a4⟩
      rw [a3, pow_succ]; field_simp; ring
    · obtain ⟨a1, a2, a3, a4⟩ := ih lo ((lo + hi) / 2) (by linarith) hlo hm
      refine ⟨a1, a2, ?_, a4⟩
      rw [a3, pow_succ]; field_simp; ring

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- Theorem 3(d): the MaxStableEta found with the grid test never exceeds the exact one. -/
theorem maxStableEta_grid_le {F : ℝ → ℝ → ℝ} {ρ₀ : ℝ} (hF : ∀ r, Monotone fun η => F η r) {x : ℕ → ℝ} {N : ℕ}
    {lo hi : ℝ} (hlo : StabGrid F ρ₀ x N lo) (hhi : ¬ Stab F ρ₀ hi) :
    sSup {η | StabGrid F ρ₀ x N η} ≤ sSup {η | Stab F ρ₀ η} := by
  have hbdd : BddAbove {η | Stab F ρ₀ η} := ⟨hi, fun η hη => by
    by_contra hlt; push Not at hlt; exact hhi (stab_antitone hF hlt.le hη)⟩
  exact csSup_le_csSup hbdd ⟨lo, hlo⟩ fun η hη => stabGrid_imp_stab hη

end RetryTimeout
