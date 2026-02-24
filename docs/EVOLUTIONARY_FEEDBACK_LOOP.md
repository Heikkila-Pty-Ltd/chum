# Evolutionary Feedback Loop — Machine-Speed Learning

## Timeline: Minutes & Hours, Not Months

This is an **AI-powered evolutionary system** operating at machine speed. Improvement cycles are measured in **hours and days**, not weeks or months.

---

## Feedback Loop Cadence

### 🔄 Real-Time (< 1 minute)
**Event:** Morsel completes (success or failure)

```
Dispatch completes
    ↓
RecordOutcomeActivity stores DoD result
    ↓
Learner workflow spawns (IMMEDIATELY)
    ↓
ExtractLessonsActivity extracts antibodies
    ↓
Antibodies stored in genome (< 30 seconds)
    ↓
NEXT dispatch gets updated genome
```

**Result:** Antibody created and available for next shark **within 1 minute**

---

### 🦴 Every 30 Minutes
**Event:** Paleontologist scheduled run

```
PaleontologistWorkflow executes
    ↓
Step 1: Provider fitness analysis
Step 2: Antibody discovery (UBS patterns)
Step 3: Protein candidates scan
Step 4: Species health audit
Step 5: Cost trend analysis
Step 6: Recurring DoD failure detection ← NEW
Step 7: Failure rate trend analysis ← NEW
    ↓
Matrix alerts sent if issues detected
    ↓
Health events recorded
```

**Result:** Systemic issues detected and raised **within 30 minutes**

---

## Expected Improvement Timeline

### Hour 1: Initial Baseline
```
Failure rate: 40% (no antibodies yet)
Recurring failures: 0 detected (insufficient data)
Genomes: Empty or minimal
```

### Hours 2-4: First Learning Cycle
```
✅ 3-5 antibodies created from initial failures
✅ Recurring pattern detected (same build error × 3)
✅ Matrix alert sent to admin
✅ Failure rate: 35% (5% improvement)
```

### Hours 6-12: Antibodies Accumulating
```
✅ 10-15 antibodies accumulated
✅ Genomes evolving (3+ generations for active species)
✅ Recurring patterns identified and antibodies injected
✅ Failure rate: 28% (12% improvement from baseline)
✅ First species shows 2x success rate vs newborns
```

### Day 2: Learning Stabilizing
```
✅ 25-40 antibodies across all species
✅ Genomes at generation 5-10
✅ Failure rate: 18% (22% improvement)
✅ 80% of failures are "new" errors (not repeats)
✅ Matrix alerts shift from "systemic issues" to "new patterns"
```

### Days 3-7: Mature State
```
✅ 50+ antibodies, well-organized genomes
✅ Failure rate: 8-12% (stabilized)
✅ 95% of failures are novel (not recurring)
✅ Calcified patterns start replacing LLM for repeated tasks
✅ Cost-per-success drops as cheap deterministic scripts take over
```

### Weeks 2-4: Optimization Phase
```
✅ Failure rate < 10% (only novel/complex errors)
✅ Genomes hibernating (species solved)
✅ Proteins created for high-success species
✅ New work bypasses LLM when calcified script exists
✅ Margin improving: deterministic > stochastic
```

---

## Measurement Cadence

| Metric | Check Frequency | Expected Improvement |
|--------|----------------|---------------------|
| **Recurring failures** | Every 30 min | Detected within 30 min of 3rd occurrence |
| **Failure rate trend** | Every 30 min | 5-10% drop per 24h in first 3 days |
| **Antibody count** | Every dispatch | +2-5 per hour during active work |
| **Genome generations** | Every dispatch | Generation 5+ within 12 hours for active species |
| **Species success rate** | Hourly | 2x improvement within 24h for species with genome |

---

## Why Hours, Not Months?

### Human Systems (Slow)
- Meetings, approvals, deployments: **days to weeks**
- Learning from failures: **quarterly retros**
- Process improvement: **6-12 months**

### AI Evolutionary System (Fast)
- Feedback loop: **< 1 minute** (learner spawns immediately)
- Pattern detection: **30 minutes** (paleontologist runs)
- Genome evolution: **per dispatch** (seconds)
- Antibody injection: **next dispatch** (immediate)

---

## Real-Time Monitoring

### Every 30 Minutes
Run the CLI report to see improvement:
```bash
./scripts/failure-rate-report.sh
```

Expected output after 6 hours:
```
📊 Last 24 Hours
   Total DoD checks: 42
   Passed: 28
   Failed: 14
   Failure rate: 33.3%  ← Down from 40% baseline

📈 Daily Trend (Last 7 Days)
Day          Total  Failed  Failure Rate
2026-02-23   42     14      33.3%  ← Today: improving
2026-02-22   8      4       50.0%  ← Yesterday: baseline

🚨 Recurring Failures (3+ occurrences in last 24h)
   ✅ No recurring failures detected  ← Antibodies working!
```

### After 24 Hours
```
📊 Last 24 Hours
   Total DoD checks: 95
   Passed: 75
   Failed: 20
   Failure rate: 21.1%  ← 19% improvement from baseline!

🦴 Recent Paleontologist Runs
Run At                Recurring  Antibodies  Audited
2026-02-23 14:30     0          12          8
2026-02-23 14:00     1          8           6
2026-02-23 13:30     2          5           4

📋 Recent Health Events
Time                  Type                      Details
2026-02-23 14:30     failure_rate_improving    40.0% → 21.1% (-18.9% points)
```

---

## Acceleration Factors

### What Makes It Fast?

1. **No human in the loop** for learning
   - Antibodies auto-extracted
   - Genomes auto-evolved
   - Next dispatch gets updates immediately

2. **Continuous operation**
   - Paleontologist runs every 30 min (48× per day)
   - Learner spawns after every completion
   - No "business hours" — 24/7 evolution

3. **Parallel learning**
   - Multiple species evolving simultaneously
   - Cross-species pattern detection
   - Global antibody library

4. **Deterministic acceleration**
   - Calcified patterns bypass LLM entirely
   - 10-100× faster than stochastic execution
   - Near-zero cost for repeated tasks

---

## When to Worry

### Red Flags (Check Within 24 Hours)

❌ **Failure rate NOT improving after 12 hours**
- Check: Are antibodies being created? (`SELECT COUNT(*) FROM genomes WHERE antibodies != '[]'`)
- Check: Are genomes being injected? (look for "Genome injected" in logs)
- Action: Debug learner workflow

❌ **Same error recurring 5+ times**
- Check: Did paleontologist send alert?
- Check: Was antibody created? (`SELECT * FROM genomes WHERE species = 'failing-species'`)
- Action: Manual investigation + create antibody if missing

❌ **No paleontologist runs in 1 hour**
- Check: Temporal schedule active?
- Action: Restart Temporal worker

---

## Success Pattern (First 48 Hours)

### Hour 0-6: Discovery
- Baseline failure rate established
- Initial antibodies created
- Recurring patterns detected

### Hour 6-12: Learning
- Antibodies prevent repeat failures
- Failure rate drops 5-10%
- Genomes reach generation 3-5

### Hour 12-24: Stabilization
- Failure rate drops 15-20%
- Most recurring patterns eliminated
- Species success rates diverge (good genomes win)

### Hour 24-48: Optimization
- Failure rate drops 25-30%
- New failures are genuinely novel
- First calcified patterns created

### Day 3-7: Mature
- Failure rate < 15%
- Genomes hibernating (solved species)
- System running mostly on deterministic scripts

---

## Comparison: Before vs After

### Before (No Evolutionary Learning)
```
Hour 1:  40% failure rate
Hour 6:  40% failure rate (no learning)
Hour 12: 40% failure rate (same mistakes repeated)
Hour 24: 40% failure rate (stagnant)
```

### After (With Evolutionary Learning)
```
Hour 1:  40% failure rate (baseline)
Hour 6:  33% failure rate (antibodies working)
Hour 12: 25% failure rate (genomes evolving)
Hour 24: 18% failure rate (learning stabilized)
Day 7:   8% failure rate (mature system)
```

**The difference is visible within hours, not months.**

---

## Next Steps

1. **Hour 1**: Monitor first paleontologist run (check Matrix)
2. **Hour 6**: Run failure-rate-report.sh (expect 5-10% improvement)
3. **Hour 12**: Check recurring failures (should drop from 5+ to <3)
4. **Day 1**: Review daily trend (expect 15-20% improvement)
5. **Day 3**: Validate genomes (query before/after evolution)
6. **Week 1**: Celebrate <15% failure rate 🎉

This is **machine-speed evolution**. If you don't see improvement within 24 hours, something is broken. If you DO see improvement, the system is working as designed. 🚀
