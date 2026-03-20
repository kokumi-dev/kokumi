---
title: kokumi
layout: hextra-home
---

<div style="display:flex;align-items:center;gap:4rem;padding:3rem 0;flex-wrap:wrap;">
  <div style="flex:1;min-width:320px;">
    <h1 style="font-size:3.5rem;font-weight:700;line-height:1.1;margin-bottom:1.5rem;">
      Structured, Immutable Release Management for Kubernetes
    </h1>
    <p style="font-size:1.375rem;line-height:1.8;margin-bottom:2.5rem;opacity:0.8;">
      Kokumi separates build intent from immutable artifacts and active state —
      so your platform team ships with confidence, every time.
    </p>
    {{< hextra/hero-button text="Get Started" link="/docs/getting-started" >}}
    {{< hextra/hero-button text="View on GitHub" link="https://github.com/kokumi-dev/kokumi" style="secondary" >}}
  </div>
  <div style="flex:1;min-width:320px;display:flex;justify-content:center;align-items:center;">
    <img src="/images/kokumi.png" alt="kokumi logo" style="max-width:640px;width:100%;height:auto;" />
  </div>
</div>

<div style="margin:3rem 0;">
  <img src="/images/screenshot.png" alt="Kokumi UI screenshot" style="width:100%;height:auto;border-radius:8px;box-shadow:0 4px 24px rgba(0,0,0,0.12);" />
</div>

<div style="margin:3rem 0 1.5rem;width:100%;text-align:center;">
  <h2 style="font-size:3rem;font-weight:700;">Built for Confidence at Scale</h2>
</div>

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="Immutable Artifacts"
    subtitle="Every render produces a content-addressed OCI artifact. A Preparation is created once and never modified — giving you a permanent, reproducible history of everything ever shipped."
  >}}
  {{< hextra/feature-card
    title="Instant Rollback"
    subtitle="Roll back by pointing the Serving at any previous Preparation. The artifact already exists in the registry — no re-render, no rebuild, no waiting."
  >}}
  {{< hextra/feature-card
    title="Approval Gates"
    subtitle="Rendering and deployment are fully decoupled. Inspect the complete rendered manifest in the built-in UI before promoting, or require explicit human sign-off between environments."
  >}}
  {{< hextra/feature-card
    title="Drift Detection"
    subtitle="The deployed SHA-256 digest is compared on every sync. Any mismatch between desired and running is a concrete, actionable signal — not an ambiguous diff."
  >}}
  {{< hextra/feature-card
    title="Air-Gap Ready"
    subtitle="The entire pipeline runs offline. All dependencies are OCI artifacts that can be mirrored into your private registry in advance — no external connectivity required at deploy time."
  >}}
  {{< hextra/feature-card
    title="GitOps Native"
    subtitle="Kokumi delegates runtime deployment to Argo CD. It feeds your existing GitOps workflow rather than replacing it, so your Argo CD dashboards, policies, and RBAC stay intact."
  >}}
{{< /hextra/feature-grid >}}
