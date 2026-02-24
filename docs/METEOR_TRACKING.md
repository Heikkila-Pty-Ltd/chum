# ☄️ Meteor Tracking — Extinction Event Risk Assessment

## Overview

The **Meteor Tracking System** monitors the evolutionary ecosystem for extinction-level threats. As failure rates degrade, a metaphorical meteor approaches the ecosystem. Multiple consecutive failures = **extinction event in progress**.

This provides **escalating warnings to Hex** (the scrum master) without being a hard gate. Hex makes the strategic decision on whether to shelter organisms (pause dispatching).

---

## The Meteor Metaphor

| Risk Level | Meteor Status | Distance | Meaning |
|------------|---------------|----------|---------|
| 🌍 **GREEN** | Distant | `🌍........................................☄️` | Ecosystem thriving, evolution continues |
| ⚠️ **YELLOW** | Approaching | `🌍............................☄️` | Atmospheric anomaly detected |
| 🔶 **ORANGE** | Incoming | `🌍..............☄️` | Impact imminent, species at risk |
| 🚨 **RED** | Near Impact | `🌍....☄️` | Extinction risk critical |
| 💥 **RED** | EXTINCTION EVENT | `🌍💥` | Mass extinction in progress |

---

## How It Works

### Every 30 Minutes (Paleontologist Scan)

```
1. Measure failure rate trend (DoD pass/fail rates)
2. Record health event (degrading/improving/stable)
3. Calculate degradation streak (consecutive bad periods)
4. Compute ecosystem health score (0-100)
5. Update meteor distance
6. Send alert to Hex with escalating urgency
```

### Meteor Movement

**Approaching (Each Degrading Period):**
- Score: **-15 points** per consecutive degrading period
- Distance: Meteor gets closer to ecosystem
- Alert escalates: Green → Yellow → Orange → Red

**Departing (Any Improvement):**
- Score: **Instant reset to 100**
- Distance: Meteor returns to distant position
- Alert resets to Green

---

## Matrix Alert Format (Sent to Hex)

### 🌍 Green (Ecosystem Thriving)
```
🌍 **ECOSYSTEM THRIVING** — Meteor Tracking Report

☄️ **Meteor Status:** Distant
📏 **Distance:** `🌍........................................☄️`
📊 **Ecosystem Health:** 100/100 (green)
📉 **Degradation Streak:** 0 consecutive impact warnings
📈 **Recovery Streak:** 2 consecutive improvements

**Current Species Mortality Rate:** 12.5% (5 extinct / 40 organisms)
**Previous Mortality Rate:** 18.0% (9 extinct / 50 organisms)
**Atmospheric Change:** -5.5% points (improving)

**Paleontologist Assessment for Hex:**
Ecosystem thriving - evolution continues normally

**Analysis Window:** Last 6h vs previous 6h
**Next Scan:** 30 minutes
```

### ⚠️ Yellow (Meteor Approaching)
```
☄️ **METEOR DETECTED - Approaching** — Meteor Tracking Report

☄️ **Meteor Status:** Approaching
📏 **Distance:** `🌍............................☄️`
📊 **Ecosystem Health:** 85/100 (yellow)
📉 **Degradation Streak:** 1 consecutive impact warnings
📈 **Recovery Streak:** 0 consecutive improvements

**Current Species Mortality Rate:** 28.0% (14 extinct / 50 organisms)
**Previous Mortality Rate:** 22.0% (11 extinct / 50 organisms)
**Atmospheric Change:** +6.0% points (degrading)

**Paleontologist Assessment for Hex:**
Meteor detected - monitor atmospheric conditions closely

**Analysis Window:** Last 6h vs previous 6h
**Next Scan:** 30 minutes
```

### 🔶 Orange (Meteor Incoming)
```
⚠️ **METEOR INCOMING - Impact Risk** — Meteor Tracking Report

☄️ **Meteor Status:** Incoming
📏 **Distance:** `🌍..............☄️`
📊 **Ecosystem Health:** 55/100 (orange)
📉 **Degradation Streak:** 3 consecutive impact warnings
📈 **Recovery Streak:** 0 consecutive improvements

**Current Species Mortality Rate:** 42.0% (21 extinct / 50 organisms)
**Previous Mortality Rate:** 35.0% (17 extinct / 49 organisms)
**Atmospheric Change:** +7.0% points (degrading)

**Paleontologist Assessment for Hex:**
⚠️ Meteor incoming - species should prepare for impact. Consider sheltering low-priority organisms.

**Analysis Window:** Last 6h vs previous 6h
**Next Scan:** 30 minutes
```

### 💥 Red (Extinction Event)
```
💥 **☠️ EXTINCTION EVENT IN PROGRESS** — Meteor Tracking Report

☄️ **Meteor Status:** 💥 EXTINCTION EVENT
📏 **Distance:** `🌍💥`
📊 **Ecosystem Health:** 10/100 (red)
📉 **Degradation Streak:** 6 consecutive impact warnings
📈 **Recovery Streak:** 0 consecutive improvements

**Current Species Mortality Rate:** 65.0% (32 extinct / 49 organisms)
**Previous Mortality Rate:** 58.0% (29 extinct / 50 organisms)
**Atmospheric Change:** +7.0% points (degrading)

**Paleontologist Assessment for Hex:**
☠️ EXTINCTION EVENT IN PROGRESS: Stop spawning organisms until ecosystem stabilizes

**Analysis Window:** Last 6h vs previous 6h
**Next Scan:** 30 minutes
```

---

## Hex's Response Framework

### 🌍 Green (Distant)
- **Action:** None - evolution continues
- **Spawning:** Full speed
- **Monitoring:** Passive (every 30 min automatic scans)

### ☄️ Yellow (Approaching)
- **Action:** Increase monitoring
- **Spawning:** Continue, but watch for patterns
- **Investigation:** Check recurring DoD failures, verify antibodies being created

### 🔶 Orange (Incoming)
- **Action:** Prepare for impact
- **Spawning:** Shelter low-priority organisms (pause P2/P3, continue P0/P1)
- **Investigation:**
  - Same error hitting multiple species? (systemic)
  - Recent environmental change? (bad merge, dependency update)
  - Infrastructure failure? (service down)

### 🚨 Red (Near Impact / Extinction Event)
- **Action:** **SHELTER ALL ORGANISMS**
- **Spawning:** STOP (except critical survival tasks)
- **Investigation:** Emergency
  1. Identify extinction cause (what's killing all species?)
  2. Fix atmospheric anomaly (don't just restart - actually fix)
  3. Test with 1-2 organisms before resuming spawning
  4. Manually reset meteor distance once stabilized

---

## CLI Report

```bash
./scripts/failure-rate-report.sh
```

Output:
```
☄️ Meteor Tracking (Extinction Risk Assessment)
   🔶 ORANGE - Meteor Incoming
   Distance: 🌍..............☄️
   Status: 2 consecutive warnings - prepare for impact

📊 Last 24 Hours
   Total DoD checks: 95
   Passed: 55
   Failed: 40
   Failure rate: 42.1%
```

---

## Extinction Scenarios

### Scenario 1: Atmospheric Contamination (Broken Dependency)
```
11:00 - npm update contaminates atmosphere
11:30 - Species mortality 40% (was 15%)
        ☄️ YELLOW - Meteor Approaching
        🌍............................☄️

12:00 - Still contaminated, recurring pattern detected
        🔶 ORANGE - Meteor Incoming
        🌍..............☄️
        Hex investigates: finds toxic @next/font package

12:30 - Hex purges contamination (rolls back package.json)
13:00 - Atmosphere cleared, mortality 10%
        🌍 GREEN - Meteor Distant
        🌍........................................☄️
```

### Scenario 2: False Alarm (Harsh Environment = Harder Work Batch)
```
14:00 - New environment with complex challenges
14:30 - Higher mortality (35% vs 20%) - work genuinely harder
        ☄️ YELLOW - Meteor Approaching

15:00 - Antibodies forming, genomes adapting
        Mortality improving to 28%
        🌍 GREEN - Meteor retreating (adaptation working)
```

### Scenario 3: Extinction Event (Evolutionary System Broken)
```
10:00 - Learner stops spawning (Temporal worker crashed)
10:30 - No antibodies = repeated extinctions
        Mortality 45%
        ☄️ YELLOW

11:00 - Same extinction patterns repeating
        Mortality 50%
        🔶 ORANGE

11:30 - No adaptation happening
        Mortality 55%
        🚨 RED - Near Impact

12:00 - Mass extinction
        Mortality 65%
        💥 EXTINCTION EVENT
        🌍💥
        Hex: STOP spawning, fix evolutionary system

12:30 - Hex restarts Temporal worker, learner active
        Verifies antibodies forming
        Manually resets meteor (ecosystem recovering)

13:00 - Adaptation working again
        Mortality 30%
        🌍 GREEN
```

---

## Thematic Consistency

### Evolutionary System Terms

| Standard Term | Meteor Theme | Meaning |
|---------------|--------------|---------|
| Failure rate | Species mortality rate | % of organisms that don't survive |
| DoD check | Organism viability test | Can this organism survive? |
| Degrading trend | Meteor approaching | Environmental threat increasing |
| Improving trend | Meteor departing | Ecosystem recovering |
| Recurring failures | Repeated extinctions | Same species dying to same cause |
| Antibodies | Survival adaptations | Evolutionary responses to threats |
| Genomes | Species DNA | Accumulated survival knowledge |
| Paleontologist | Fossil record analyst | Studies past extinctions |
| Hex | Ecosystem guardian | Decides when to shelter species |

---

## Why "Meteor" Works

### Fits the Evolutionary Theme
- **Species/organisms** — Living things in an ecosystem
- **Genomes/antibodies** — Evolutionary adaptations
- **Sharks/organisms** — Predators and prey
- **Paleontologist** — Studies extinctions (K-T boundary = asteroid impact)
- **Meteor** — External threat to ecosystem survival

### Clear Mental Model
- **Distant** = Safe, no action needed
- **Approaching** = Heads up, something's wrong
- **Incoming** = Prepare for damage
- **Impact** = Extinction happening NOW

### Emotional Weight
- "Failure rate degrading" = Technical, boring
- "Meteor incoming, extinction risk" = Visceral, urgent
- "EXTINCTION EVENT" = Can't ignore this

---

## Success Metrics

- **Green >80% of time** = Healthy ecosystem
- **Yellow 10-15% of time** = Normal environmental variation
- **Orange <5% of time** = Occasional threats, resolved quickly
- **Red <1% of time** = Rare emergencies only
- **Extinction Events = 0** = Catch threats at orange/yellow

If meteor frequently at red, the evolutionary system needs tuning or infrastructure is unstable.

---

## Manual Meteor Reset (For Hex)

After fixing an extinction cause:

```sql
-- Clear recent impact warnings
DELETE FROM health_events
WHERE event_type = 'failure_rate_degrading'
  AND created_at > datetime('now', '-2 hours');

-- Record ecosystem recovery
INSERT INTO health_events (event_type, details)
VALUES ('failure_rate_improving', 'Meteor deflected: [describe fix]');
```

This resets the meteor to **Distant** and ecosystem health to **100**.

---

## The Paleontologist's Role

The paleontologist is uniquely suited for this role because:
- Studies **mass extinctions** (understands catastrophic failure)
- Analyzes **fossil records** (learns from past organism deaths)
- Identifies **extinction patterns** (recurring species mortality)
- Warns of **atmospheric changes** (environmental threats)

When the paleontologist sees a meteor approaching, it's saying:
> "I've seen this pattern before. In the fossil record, when species mortality rises this fast, it precedes mass extinction. Prepare accordingly."

The meteor metaphor makes the paleontologist's warnings visceral and urgent. 🦴☄️
