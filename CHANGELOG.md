# Changelog

## [0.11.0](https://github.com/kokumi-dev/kokumi/compare/0.10.0...0.11.0) (2026-04-19)


### Features

* add manifest preview in create and update order ([#130](https://github.com/kokumi-dev/kokumi/issues/130)) ([ee6eeaa](https://github.com/kokumi-dev/kokumi/commit/ee6eeaaf28fae486a0d4065a662a0d064416d7e3))
* enforce immutability of PreparationSpec using CEL validation ([#112](https://github.com/kokumi-dev/kokumi/issues/112)) ([9425b21](https://github.com/kokumi-dev/kokumi/commit/9425b219d1ed6a5b9e3f0f6021be7e980aae455e))
* exclude test helm hooks in rendered manifest ([#129](https://github.com/kokumi-dev/kokumi/issues/129)) ([78f8fbd](https://github.com/kokumi-dev/kokumi/commit/78f8fbdfeee309f414c845a525b5fefd1a4d96f1))

## [0.10.0](https://github.com/kokumi-dev/kokumi/compare/0.9.1...0.10.0) (2026-04-12)


### Features

* add commit message support for Order creation and updates ([#107](https://github.com/kokumi-dev/kokumi/issues/107)) ([d32ff53](https://github.com/kokumi-dev/kokumi/commit/d32ff5311afe80755c4cf887d82011b52ab4f66c))
* disable save changes button when no changes are present ([#109](https://github.com/kokumi-dev/kokumi/issues/109)) ([d901813](https://github.com/kokumi-dev/kokumi/commit/d901813281502443aa4185253ee59c447e3d0b0f))
* link preparations trough parent digest revision chain ([#111](https://github.com/kokumi-dev/kokumi/issues/111)) ([744f99b](https://github.com/kokumi-dev/kokumi/commit/744f99b40f1379f5266b7bc328ec167272728c1e))

## [0.9.1](https://github.com/kokumi-dev/kokumi/compare/0.9.0...0.9.1) (2026-04-06)


### Bug Fixes

* include helm hooks for helm renderer in rendered manifest ([#101](https://github.com/kokumi-dev/kokumi/issues/101)) ([1cef450](https://github.com/kokumi-dev/kokumi/commit/1cef45043e49df6868599f32fcdd19928b2a8e68))

## [0.9.0](https://github.com/kokumi-dev/kokumi/compare/0.8.0...0.9.0) (2026-03-20)


### Features

* add support for ui driven edits for orders ([#89](https://github.com/kokumi-dev/kokumi/issues/89)) ([619c494](https://github.com/kokumi-dev/kokumi/commit/619c4944e6ff99525e121b6be4265ff7323467c9))

## [0.8.0](https://github.com/kokumi-dev/kokumi/compare/0.7.0...0.8.0) (2026-03-19)


### Features

* add crd filter in ui ([#85](https://github.com/kokumi-dev/kokumi/issues/85)) ([c43e858](https://github.com/kokumi-dev/kokumi/commit/c43e8584b6e11d0fbfbf64b6a18f23693045bf36))
* add default in-cluster registry for order destination ([#84](https://github.com/kokumi-dev/kokumi/issues/84)) ([7fd47a7](https://github.com/kokumi-dev/kokumi/commit/7fd47a796a4b4f474514b6f3b5a89ac2206f0652))
* add menu crd ([#79](https://github.com/kokumi-dev/kokumi/issues/79)) ([0bdd765](https://github.com/kokumi-dev/kokumi/commit/0bdd7658f186fbed655a8cfc035f27c9fcc1ffb8))
* implement menu crd and ui for creating reusable templates ([#83](https://github.com/kokumi-dev/kokumi/issues/83)) ([d32765b](https://github.com/kokumi-dev/kokumi/commit/d32765b6adf09fb9ef5fba11d73434068dc7933b))

## [0.7.0](https://github.com/kokumi-dev/kokumi/compare/0.6.1...0.7.0) (2026-03-17)


### ⚠ BREAKING CHANGES

* rename recipe crd to order and update related ui components ([#74](https://github.com/kokumi-dev/kokumi/issues/74))

### Code Refactoring

* rename recipe crd to order and update related ui components ([#74](https://github.com/kokumi-dev/kokumi/issues/74)) ([f11d482](https://github.com/kokumi-dev/kokumi/commit/f11d482da77448c1cecd727111c8ac6e29d93f79))

## [0.6.1](https://github.com/kokumi-dev/kokumi/compare/0.6.0...0.6.1) (2026-03-11)


### Bug Fixes

* serving deploying status did not update ([#68](https://github.com/kokumi-dev/kokumi/issues/68)) ([7018fc0](https://github.com/kokumi-dev/kokumi/commit/7018fc024b7111eba05623f92fe47867db247644))

## [0.6.0](https://github.com/kokumi-dev/kokumi/compare/0.5.3...0.6.0) (2026-03-05)


### Features

* add helm rendering support ([#57](https://github.com/kokumi-dev/kokumi/issues/57)) ([2b60801](https://github.com/kokumi-dev/kokumi/commit/2b60801b32791102f037c11d2f365b27ce58e23e))

## [0.5.3](https://github.com/kokumi-dev/kokumi/compare/0.5.2...0.5.3) (2026-03-04)


### Bug Fixes

* use full recipe spec for config hash ([#55](https://github.com/kokumi-dev/kokumi/issues/55)) ([8c9bd8e](https://github.com/kokumi-dev/kokumi/commit/8c9bd8ef26154b20a14e9c5d2401e93ef4d185e6))

## [0.5.2](https://github.com/kokumi-dev/kokumi/compare/0.5.1...0.5.2) (2026-03-04)


### Bug Fixes

* issue with creation of multiple preparations ([#51](https://github.com/kokumi-dev/kokumi/issues/51)) ([04bc133](https://github.com/kokumi-dev/kokumi/commit/04bc1338e0f1ea1dcaa3fb6e107130109bdb1816))

## [0.5.1](https://github.com/kokumi-dev/kokumi/compare/0.5.0...0.5.1) (2026-03-03)


### Bug Fixes

* api group in sidebar ([#46](https://github.com/kokumi-dev/kokumi/issues/46)) ([0497ad6](https://github.com/kokumi-dev/kokumi/commit/0497ad67bdda3e8162d9f167617e4e10354ac5db))

## [0.5.0](https://github.com/kokumi-dev/kokumi/compare/0.4.0...0.5.0) (2026-03-03)


### Features

* add preparations and servings ui ([#36](https://github.com/kokumi-dev/kokumi/issues/36)) ([31129f2](https://github.com/kokumi-dev/kokumi/commit/31129f2295bd3ddc3a3e5143f963e36266538dc7))
* implement recipe management ui ([#35](https://github.com/kokumi-dev/kokumi/issues/35)) ([3f90db2](https://github.com/kokumi-dev/kokumi/commit/3f90db2f4c49f0fd9435ca672a0d9d3cd93787dd))
* implement SSE for resource counts and update dashboard ([#33](https://github.com/kokumi-dev/kokumi/issues/33)) ([ae38537](https://github.com/kokumi-dev/kokumi/commit/ae385378a756cc60b1e6e2e68c2af118c9e8d1a8))
* use server side apply in argocd applications ([#37](https://github.com/kokumi-dev/kokumi/issues/37)) ([966ba8a](https://github.com/kokumi-dev/kokumi/commit/966ba8a08e41bfd8127b857bae453714d9f4a426))

## [0.4.0](https://github.com/kokumi-dev/kokumi/compare/0.3.0...0.4.0) (2026-02-28)


### Features

* add initial implementations of controllers ([#28](https://github.com/kokumi-dev/kokumi/issues/28)) ([9f3dd5b](https://github.com/kokumi-dev/kokumi/commit/9f3dd5b851092d798987a5598b84b19f9dffb614))
* add server component and initial ui ([#30](https://github.com/kokumi-dev/kokumi/issues/30)) ([e9568d1](https://github.com/kokumi-dev/kokumi/commit/e9568d11984475d04c93221aa0858bfa5fed19d6))
* implement initial ui concept ([#31](https://github.com/kokumi-dev/kokumi/issues/31)) ([c4e66fd](https://github.com/kokumi-dev/kokumi/commit/c4e66fdc6f6b0dad82f0ad6e1636fb750b3915ef))
* return version in info endpoint set by release process ([#32](https://github.com/kokumi-dev/kokumi/issues/32)) ([bcdc26a](https://github.com/kokumi-dev/kokumi/commit/bcdc26a6f34bb63e6d34f9240c21ba94940f9807))

## [0.3.0](https://github.com/kokumi-dev/kokumi/compare/0.2.0...0.3.0) (2026-02-25)


### Features

* add initial api spec to crds ([#23](https://github.com/kokumi-dev/kokumi/issues/23)) ([658d939](https://github.com/kokumi-dev/kokumi/commit/658d939ddefc85243da89d059f1d37cce7340831))
* add registry to store oci artefacts ([#26](https://github.com/kokumi-dev/kokumi/issues/26)) ([7b264a8](https://github.com/kokumi-dev/kokumi/commit/7b264a8cad597e112a3b5aae33e48262a18bdb50))
* drop system suffix from namespace ([#25](https://github.com/kokumi-dev/kokumi/issues/25)) ([935c82e](https://github.com/kokumi-dev/kokumi/commit/935c82e01ce3f15f9eb9552921a4302782656658))

## [0.2.0](https://github.com/kokumi-dev/kokumi/compare/0.1.0...0.2.0) (2026-02-25)


### Miscellaneous Chores

* release 0.2.0 ([#21](https://github.com/kokumi-dev/kokumi/issues/21)) ([5f14fd6](https://github.com/kokumi-dev/kokumi/commit/5f14fd64e23d2ba389f56690e7343b194b3ff647))

## 0.1.0 (2026-02-24)


### Features

* add crds for recipe, preparation and serving ([#2](https://github.com/kokumi-dev/kokumi/issues/2)) ([58c1bb7](https://github.com/kokumi-dev/kokumi/commit/58c1bb77491df16728588290c600f1f4a595a22f))
* init kokumi with kubebuilder ([#1](https://github.com/kokumi-dev/kokumi/issues/1)) ([fe6a3c3](https://github.com/kokumi-dev/kokumi/commit/fe6a3c34b3de302353b19555047e9ac8a0c17631))


### Miscellaneous Chores

* release 0.1.0 ([#16](https://github.com/kokumi-dev/kokumi/issues/16)) ([5014e81](https://github.com/kokumi-dev/kokumi/commit/5014e8141b6ee8cab9079e48cd08819b599661fd))
