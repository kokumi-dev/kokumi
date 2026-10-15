import Section from '../../components/layout/Section'
import PropertyList from '../../components/layout/PropertyList'
import ConditionList from '../../components/layout/ConditionList'
import PatchList from '../../components/shared/PatchList'
import { Counter } from '../../components/ui'
import { formatDate } from '../../utils/format'
import { objectToYaml } from '../../utils/yaml'
import {
  allowedTargetLabel,
  describePatchesPolicy,
  describeValuesPolicy,
  patchesPolicy,
  valuesPolicy,
} from '../../utils/products'
import type { OverridePolicy } from '../../api/types'
import { useProductContext, vendorLabel } from './productFormat'
import layout from '../../components/layout/layout.module.css'

export default function ProductOverview() {
  const { product } = useProductContext()
  const { source, resolvedSource, render, overrides } = product
  const helm = render?.helm
  const baseValues = objectToYaml(helm?.values ?? {})

  return (
    <>
      <Section title="Source">
        <PropertyList
          items={[
            {
              label: 'Source',
              value: <span className={layout.mono}>{source.pantryRef?.name ? `pantry: ${source.pantryRef.name}` : source.oci}</span>,
            },
            { label: 'Version', value: source.version },
            {
              label: 'Vendoring',
              value: product.vendor && <span className={layout.mono}>{vendorLabel(product.vendor)}</span>,
              show: !!product.vendor,
            },
            { label: 'Created', value: formatDate(product.createdAt) },
          ]}
        />
      </Section>

      <Section
        title="Resolved source"
        description={product.vendor ? 'The vendored copy that Orders render from' : 'What Orders render from'}
      >
        {resolvedSource ? (
          <PropertyList
            items={[
              { label: 'OCI', value: <span className={layout.mono}>{resolvedSource.oci}</span> },
              { label: 'Version', value: resolvedSource.version },
              {
                label: 'Digest',
                value: resolvedSource.digest
                  ? <span className={layout.mono}>{resolvedSource.digest}</span>
                  : <span className={layout.muted}>Not resolved; the registry could not be reached</span>,
              },
              { label: 'Pantry', value: resolvedSource.pantryRef?.name, show: !!resolvedSource.pantryRef },
            ]}
          />
        ) : (
          <span className={layout.muted}>Not resolved yet. See the conditions below.</span>
        )}
      </Section>

      <Section title="Render">
        <PropertyList
          items={[
            { label: 'Renderer', value: helm ? 'Helm' : 'Manifest' },
            { label: 'Release name', value: helm?.releaseName, show: !!helm?.releaseName },
            { label: 'Helm namespace', value: helm?.namespace, show: !!helm?.namespace },
            { label: 'Include CRDs', value: helm?.includeCRDs ? 'Yes' : 'No', show: !!helm },
            { label: 'Base values', value: <pre className={layout.code}>{baseValues}</pre>, show: !!baseValues },
            { label: 'File layout', value: render?.manifest?.layout ?? 'Single', show: !helm },
          ]}
        />
      </Section>

      {product.patches && product.patches.length > 0 && (
        <Section title={<>Base patches<Counter count={product.patches.length} /></>} description="Applied to every Order of this Product">
          <PatchList patches={product.patches} />
        </Section>
      )}

      <Section title="Overrides" description="What Orders of this Product may change">
        <PropertyList
          items={[
            {
              label: 'Helm values',
              value: (
                <PolicyValue
                  policy={valuesPolicy(product)}
                  description={describeValuesPolicy(valuesPolicy(product))}
                  listed={overrides?.values?.paths ?? []}
                />
              ),
              show: !!helm,
            },
            {
              label: 'Patches and edits',
              value: (
                <PolicyValue
                  policy={patchesPolicy(product)}
                  description={describePatchesPolicy(patchesPolicy(product))}
                  listed={(overrides?.patches?.targets ?? []).map(allowedTargetLabel)}
                />
              ),
            },
          ]}
        />
      </Section>

      <Section title="Conditions">
        <ConditionList conditions={product.conditions} />
      </Section>
    </>
  )
}

/** A policy in words, followed by what it lists when it is Listed. */
function PolicyValue({ policy, description, listed }: { policy: OverridePolicy; description: string; listed: string[] }) {
  return (
    <>
      {policy} <span className={layout.muted}>· {description}</span>
      {policy === 'Listed' && listed.map((item) => (
        <div key={item} className={layout.mono}>{item}</div>
      ))}
    </>
  )
}
