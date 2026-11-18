/-
Source: abstraction/benchmark_runner/retry_timeout/design.md (retry_timeout v0.3.0)

Theorem 1 of the Convergence section: the RetryFeedback iteration `r ← f r` from `ρ₀`, in exact arithmetic.
`f` is any function with the properties of `FeedbackHyp`; that the M/M/k RetryFeedback has them is argued in
prose in the design (assumptions H1–H5), not here.
-/
import Mathlib.Algebra.Order.Archimedean.Real.Basic
import Mathlib.Topology.Instances.Real.Lemmas
import Mathlib.Topology.Order.IntermediateValue
import Mathlib.Topology.Order.Compact
import Mathlib.Topology.Order.Monotone
import Mathlib.Topology.Order.MonotoneConvergence

namespace RetryTimeout

open Set Filter Topology

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- The assumptions on RetryFeedback `f(r) = ρ₀ (1 + Pr(sojourn > T | load r))` on `[ρ₀, 1]`, with `f 1` the
continuous extension at load `1` (assumptions H1–H5 of the design). -/
structure FeedbackHyp (f : ℝ → ℝ) (ρ₀ : ℝ) : Prop where
  /-- H1: the load without retries is in `(0, 1)`. -/
  pos : 0 < ρ₀
  lt_one : ρ₀ < 1
  /-- H2: `f` is continuous on `[ρ₀, 1]`. -/
  cont : ContinuousOn f (Icc ρ₀ 1)
  /-- H3: `f` is monotone on `[ρ₀, 1]` (the tail grows with the load). -/
  mono : MonotoneOn f (Icc ρ₀ 1)
  /-- H4: retries never lower the load at `ρ₀`. -/
  start : ρ₀ ≤ f ρ₀
  /-- H5: at load `1` every request retries. -/
  at_one : f 1 = 2 * ρ₀

/-- The iterates `x₀, f x₀, f (f x₀), …`. -/
def iter (f : ℝ → ℝ) (x₀ : ℝ) : ℕ → ℝ
  | 0 => x₀
  | n + 1 => f (iter f x₀ n)

@[simp] theorem iter_zero (f : ℝ → ℝ) (x₀ : ℝ) : iter f x₀ 0 = x₀ := rfl

@[simp] theorem iter_succ (f : ℝ → ℝ) (x₀ : ℝ) (n : ℕ) : iter f x₀ (n + 1) = f (iter f x₀ n) := rfl

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- A fixed point lies between `a` and `b` when `f` is continuous there, `a ≤ f a` and `f b ≤ b`
(intermediate value theorem on `f x - x`). -/
theorem exists_fixed_between {f : ℝ → ℝ} {a b : ℝ} (hab : a ≤ b) (hc : ContinuousOn f (Icc a b))
    (ha : a ≤ f a) (hb : f b ≤ b) : ∃ x ∈ Icc a b, f x = x := by
  have hg : ContinuousOn (fun x => f x - x) (Icc a b) := hc.sub continuousOn_id
  have h0 : (0 : ℝ) ∈ Icc (f b - b) (f a - a) := ⟨by linarith, by linarith⟩
  obtain ⟨x, hx, hx0⟩ := intermediate_value_Icc' hab hg h0
  exact ⟨x, hx, by simp only at hx0; linarith⟩

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- Theorem 1, monotone part: from `a ≤ f a`, below a fixed point `s ≥ a`, the iterates rise and never pass `s`. -/
theorem iter_mono_bdd {f : ℝ → ℝ} {a s : ℝ} (hmono : MonotoneOn f (Icc a s)) (has : a ≤ s)
    (ha : a ≤ f a) (hs : f s = s) (n : ℕ) :
    a ≤ iter f a n ∧ iter f a n ≤ iter f a (n + 1) ∧ iter f a (n + 1) ≤ s := by
  induction n with
  | zero =>
    refine ⟨le_rfl, ha, ?_⟩
    calc iter f a 1 = f a := rfl
      _ ≤ f s := hmono ⟨le_rfl, has⟩ ⟨has, le_rfl⟩ has
      _ = s := hs
  | succ n ih =>
    obtain ⟨h1, h2, h3⟩ := ih
    have hn : iter f a n ∈ Icc a s := ⟨h1, h2.trans h3⟩
    have hn1 : iter f a (n + 1) ∈ Icc a s := ⟨h1.trans h2, h3⟩
    refine ⟨h1.trans h2, hmono hn hn1 h2, ?_⟩
    calc iter f a (n + 2) = f (iter f a (n + 1)) := rfl
      _ ≤ f s := hmono hn1 ⟨has, le_rfl⟩ h3
      _ = s := hs

/-- Limits of iterates inside a closed interval where `f` is continuous are fixed points. -/
theorem fixed_of_tendsto {f : ℝ → ℝ} {a b x₀ L : ℝ} (hc : ContinuousOn f (Icc a b))
    (hmem : ∀ n, iter f x₀ n ∈ Icc a b) (hL : Tendsto (iter f x₀) atTop (𝓝 L)) : L ∈ Icc a b ∧ f L = L := by
  have hLmem : L ∈ Icc a b := isClosed_Icc.mem_of_tendsto hL (Eventually.of_forall hmem)
  have hw : Tendsto (iter f x₀) atTop (𝓝[Icc a b] L) :=
    tendsto_nhdsWithin_iff.2 ⟨hL, Eventually.of_forall hmem⟩
  have h1 : Tendsto (fun n => f (iter f x₀ n)) atTop (𝓝 (f L)) := (hc L hLmem).tendsto.comp hw
  have h2 : Tendsto (fun n => f (iter f x₀ n)) atTop (𝓝 L) := by
    have := hL.comp (tendsto_add_atTop_nat 1)
    simpa [Function.comp_def] using this
  exact ⟨hLmem, tendsto_nhds_unique h1 h2⟩

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- Theorem 1(a): if RetryFeedback has a fixed point in `[ρ₀, 1]`, there is a least one, the StableLoad `s`, and
the iteration from `ρ₀` converges to it. -/
theorem iter_tendsto_least_fixed {f : ℝ → ℝ} {ρ₀ : ℝ} (h : FeedbackHyp f ρ₀)
    (hex : ∃ x ∈ Icc ρ₀ 1, f x = x) :
    ∃ s ∈ Icc ρ₀ 1, f s = s ∧ (∀ x ∈ Icc ρ₀ 1, f x = x → s ≤ x) ∧
      Monotone (iter f ρ₀) ∧ Tendsto (iter f ρ₀) atTop (𝓝 s) := by
  set S := Icc ρ₀ 1 ∩ (fun x => f x - x) ⁻¹' {0} with hS
  have hSclosed : IsClosed S :=
    (h.cont.sub continuousOn_id).preimage_isClosed_of_isClosed isClosed_Icc isClosed_singleton
  have hSne : S.Nonempty := by
    obtain ⟨x, hx, hfx⟩ := hex
    exact ⟨x, hx, by simp [hfx]⟩
  have hSbdd : BddBelow S := ⟨ρ₀, fun x hx => hx.1.1⟩
  have hsS : sInf S ∈ S := hSclosed.csInf_mem hSne hSbdd
  set s := sInf S
  have hs_mem : s ∈ Icc ρ₀ 1 := hsS.1
  have hs_fix : f s = s := by
    have := hsS.2
    simp only [mem_preimage, mem_singleton_iff] at this
    linarith
  have hleast : ∀ x ∈ Icc ρ₀ 1, f x = x → s ≤ x := fun x hx hfx =>
    csInf_le hSbdd ⟨hx, by simp [hfx]⟩
  have hmono' : MonotoneOn f (Icc ρ₀ s) := h.mono.mono (Icc_subset_Icc le_rfl hs_mem.2)
  have hb := iter_mono_bdd hmono' hs_mem.1 h.start hs_fix
  have hmon : Monotone (iter f ρ₀) := monotone_nat_of_le_succ fun n => (hb n).2.1
  have hbdd : BddAbove (range (iter f ρ₀)) := by
    refine ⟨s, ?_⟩
    rintro _ ⟨n, rfl⟩
    exact ((hb n).2.1).trans (hb n).2.2
  have hL := tendsto_atTop_ciSup hmon hbdd
  have hmem : ∀ n, iter f ρ₀ n ∈ Icc ρ₀ 1 := fun n =>
    ⟨(hb n).1, (((hb n).2.1).trans (hb n).2.2).trans hs_mem.2⟩
  obtain ⟨hLmem, hLfix⟩ := fixed_of_tendsto h.cont hmem hL
  have hLle : (⨆ n, iter f ρ₀ n) ≤ s := ciSup_le fun n => ((hb n).2.1).trans (hb n).2.2
  have hLeq : (⨆ n, iter f ρ₀ n) = s := le_antisymm hLle (hleast _ hLmem hLfix)
  exact ⟨s, hs_mem, hs_fix, hleast, hmon, hLeq ▸ hL⟩

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- A fixed point of RetryFeedback exists in `[ρ₀, 1]` exactly when `f r ≤ r` somewhere there. This is what the
`above` test of `feedback_points` and the stability test of MaxStableEta decide. -/
theorem stable_iff_exists_le {f : ℝ → ℝ} {ρ₀ : ℝ} (h : FeedbackHyp f ρ₀) :
    (∃ x ∈ Icc ρ₀ 1, f x = x) ↔ ∃ r ∈ Icc ρ₀ 1, f r ≤ r := by
  constructor
  · rintro ⟨x, hx, hfx⟩
    exact ⟨x, hx, hfx.le⟩
  · rintro ⟨r, hr, hfr⟩
    obtain ⟨x, hx, hfx⟩ :=
      exists_fixed_between hr.1 (h.cont.mono (Icc_subset_Icc le_rfl hr.2)) h.start hfr
    exact ⟨x, ⟨hx.1, hx.2.trans hr.2⟩, hfx⟩

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- Theorem 1(c): with `2 ρ₀ ≤ 1` a StableLoad always exists. -/
theorem fixed_exists_of_two_rho_le_one {f : ℝ → ℝ} {ρ₀ : ℝ} (h : FeedbackHyp f ρ₀) (h2 : 2 * ρ₀ ≤ 1) :
    ∃ x ∈ Icc ρ₀ 1, f x = x :=
  (stable_iff_exists_le h).2 ⟨1, ⟨h.lt_one.le, le_rfl⟩, by rw [h.at_one]; exact h2⟩

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- Theorem 1(b): with `2 ρ₀ > 1` and no fixed point in `[ρ₀, 1)`, `f r - r` has a positive minimum `δ` on
`[ρ₀, 1]`, and the iteration from `ρ₀` passes `1` within `⌊(1 - ρ₀) / δ⌋ + 1` steps. -/
theorem iter_exceeds_one {f : ℝ → ℝ} {ρ₀ : ℝ} (h : FeedbackHyp f ρ₀) (h2 : 1 < 2 * ρ₀)
    (hno : ∀ x ∈ Ico ρ₀ 1, f x ≠ x) :
    ∃ δ > 0, (∀ x ∈ Icc ρ₀ 1, δ ≤ f x - x) ∧
      ∃ n ≤ ⌊(1 - ρ₀) / δ⌋₊ + 1, 1 < iter f ρ₀ n := by
  -- no fixed point on the closed interval either: f 1 = 2 ρ₀ ≠ 1
  have hno' : ∀ x ∈ Icc ρ₀ 1, f x ≠ x := by
    intro x hx hfx
    rcases eq_or_lt_of_le hx.2 with rfl | hlt
    · rw [h.at_one] at hfx; linarith
    · exact hno x ⟨hx.1, hlt⟩ hfx
  -- so f x > x on [ρ₀, 1]
  have hpos : ∀ x ∈ Icc ρ₀ 1, x < f x := by
    intro x hx
    by_contra hle
    push Not at hle
    obtain ⟨y, hy, hfy⟩ := (stable_iff_exists_le h).2 ⟨x, hx, hle⟩
    exact hno' y hy hfy
  obtain ⟨m, hm, hmin⟩ := isCompact_Icc.exists_isMinOn (nonempty_Icc.2 h.lt_one.le)
    (h.cont.sub continuousOn_id)
  set δ := f m - m with hδ
  have hδpos : 0 < δ := by have := hpos m hm; linarith
  have hδle : ∀ x ∈ Icc ρ₀ 1, δ ≤ f x - x := fun x hx => hmin hx
  refine ⟨δ, hδpos, hδle, ?_⟩
  -- while the iterates stay ≤ 1, each step adds at least δ
  have hstep : ∀ n, (∀ k < n, iter f ρ₀ k ≤ 1) → ρ₀ + n * δ ≤ iter f ρ₀ n := by
    intro n
    induction n with
    | zero => intro _; simp
    | succ n ih =>
      intro hk
      have hn := ih fun k hk' => hk k (Nat.lt_succ_of_lt hk')
      have hmem : iter f ρ₀ n ∈ Icc ρ₀ 1 :=
        ⟨le_trans (by nlinarith [hδpos.le, (Nat.cast_nonneg n : (0 : ℝ) ≤ n)]) hn, hk n (Nat.lt_succ_self n)⟩
      have := hδle _ hmem
      simp only [iter_succ, Nat.cast_succ]
      linarith
  set N := ⌊(1 - ρ₀) / δ⌋₊ + 1
  have hN : 1 < ρ₀ + N * δ := by
    have h1 : (1 - ρ₀) / δ < N := by
      simp only [N, Nat.cast_add, Nat.cast_one]
      exact Nat.lt_floor_add_one _
    have h2' : 1 - ρ₀ < N * δ := by
      rwa [div_lt_iff₀ hδpos] at h1
    linarith
  by_contra hcon
  push Not at hcon
  have := hstep N fun k hk => hcon k hk.le
  linarith [hcon N le_rfl]

-- Design: abstraction/benchmark_runner/retry_timeout/design.md#convergence
/-- Theorem 1(e), basin of the StableLoad: from a start `x₀` above a fixed point `s`, with `f r ≤ r` on
`[s, x₀]` and no other fixed point there (true below the TippingLoad), the iterates fall back to `s`. -/
theorem basin_down {f : ℝ → ℝ} {s x₀ : ℝ} (hsx : s ≤ x₀) (hc : ContinuousOn f (Icc s x₀))
    (hmono : MonotoneOn f (Icc s x₀)) (hs : f s = s) (hle : ∀ r ∈ Icc s x₀, f r ≤ r)
    (honly : ∀ r ∈ Icc s x₀, f r = r → r = s) :
    Antitone (iter f x₀) ∧ Tendsto (iter f x₀) atTop (𝓝 s) := by
  have hb : ∀ n, s ≤ iter f x₀ (n + 1) ∧ iter f x₀ (n + 1) ≤ iter f x₀ n ∧ iter f x₀ n ≤ x₀ := by
    intro n
    induction n with
    | zero =>
      refine ⟨?_, hle x₀ ⟨hsx, le_rfl⟩, le_rfl⟩
      calc s = f s := hs.symm
        _ ≤ f x₀ := hmono ⟨le_rfl, hsx⟩ ⟨hsx, le_rfl⟩ hsx
    | succ n ih =>
      obtain ⟨h1, h2, h3⟩ := ih
      have hn1 : iter f x₀ (n + 1) ∈ Icc s x₀ := ⟨h1, h2.trans h3⟩
      refine ⟨?_, hle _ hn1, h2.trans h3⟩
      calc s = f s := hs.symm
        _ ≤ f (iter f x₀ (n + 1)) := hmono ⟨le_rfl, hsx⟩ hn1 h1
  have hanti : Antitone (iter f x₀) := antitone_nat_of_succ_le fun n => (hb n).2.1
  have hmem : ∀ n, iter f x₀ n ∈ Icc s x₀ := by
    intro n
    cases n with
    | zero => exact ⟨hsx, le_rfl⟩
    | succ n => exact ⟨(hb n).1, ((hb n).2.1).trans (hb n).2.2⟩
  have hbdd : BddBelow (range (iter f x₀)) := ⟨s, by rintro _ ⟨n, rfl⟩; exact (hmem n).1⟩
  have hL := tendsto_atTop_ciInf hanti hbdd
  obtain ⟨hLmem, hLfix⟩ := fixed_of_tendsto hc hmem hL
  exact ⟨hanti, honly _ hLmem hLfix ▸ hL⟩

end RetryTimeout
