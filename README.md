<p align="center">
  <img src="docs/assets/kokumi.png" alt="Kokumi Logo" width="400" />
</p>

<h1 align="center">Kokumi</h1>

<p align="center">
  <em>
    Kokumi (/koʊkuːmi/, Japanese: コク味, from コク “richness” + 味 “taste”) means "heartiness" or
    "richness" — subtle compounds that enhance and harmonize flavors.
    <br /><br />
    Kokumi applies this idea to platform delivery: Recipes define intent,
    Preparations produce immutable artifacts, Servings activate a selected
    preparation, and Menus orchestrate them together.
  </em>
</p>

---

# Overview

**Kokumi** is a Kubernetes operator for structured, immutable release management.

It separates:

- Intent definition
- Immutable artifact rendering
- Activation of a single selected version
- Atomic coordination across multiple components

## Core Concepts

Kokumi models release workflows using a small set of CRDs.

### Recipe

Defines how something should be built or rendered.

A Recipe contains:

- source definitions
- patches or transformations
- rendering configuration

A Recipe describes intent — not a running system.

### Preparation (immutable)

Represents the rendered, immutable OCI artifact produced from a Recipe.

Properties:

- Derived from exactly one Recipe
- Immutable once created
- Multiple Preparations may exist per Recipe
- Comparable to a build artifact or release candidate

### Serving (active selection)

Represents the active deployment of exactly one Preparation.

Properties:

- Exactly one Serving per Recipe
- References one specific Preparation
- Mutable (can switch to a different Preparation)
- Represents what is currently active

This cleanly separates immutable history from active state.

### Menu (atomic coordination)

Groups multiple Recipes into a single logical unit.

A Menu allows:

- Coordinated updates
- Atomic rollouts
- Consistent activation across multiple Recipes

This enables platform-level releases composed of multiple components.

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

