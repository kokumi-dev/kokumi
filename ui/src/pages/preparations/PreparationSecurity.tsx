import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { getPreparationVulnerabilities } from '../../api/client'
import type { PreparationImageReport, PreparationVulnerabilityReport } from '../../api/types'
import Section from '../../components/layout/Section'
import PropertyList from '../../components/layout/PropertyList'
import EmptyState from '../../components/layout/EmptyState'
import ScannerSummary from '../../components/vulnerability/ScannerSummary'
import ImageScanTable from '../../components/vulnerability/ImageScanTable'
import FindingsSection from '../../components/vulnerability/FindingsSection'
import VerdictLine from '../../components/vulnerability/VerdictLine'
import ComparisonSection from '../../components/vulnerability/ComparisonSection'
import { Counter } from '../../components/ui'
import { useCan } from '../../hooks/useCapability'
import { useQueryParam } from '../../hooks/useFilterParam'
import { useServings } from '../../hooks/useServings'
import { paths } from '../../routes/paths'
import {
  checkVerdict,
  describeVulnerabilityCheck,
  scannedPreparationsHint,
  shortRef,
  type ScannedImage,
} from '../../utils/vulnerabilities'
import { usePreparationContext } from './preparationContext'
import layout from '../../components/layout/layout.module.css'

/** The reason the server gives for an image whose digest has no scan yet. */
const notScanned = 'the image has not been scanned'

/** An image of the Preparation as the table shows it: by its reference as written. */
function scannedImage(image: PreparationImageReport, withCheck: boolean): ScannedImage {
  return {
    key: image.ref,
    names: [image.ref],
    label: shortRef(image.ref),
    title: image.digest ? `${image.ref}\n${image.digest}` : image.ref,
    digest: image.digest,
    scan: image.scan,
    missing: image.missing,
    missingHint: image.missing === notScanned ? `${scannedPreparationsHint} Promoting this one scans it.` : undefined,
    blocking: withCheck ? image.blocking : undefined,
  }
}

/**
 * PreparationSecurity joins the images of the Preparation with the latest
 * scans of its Product, tells whether the vulnerability check it was
 * rendered under holds its deployment, and compares its findings with the
 * deployed Preparation's. The selected image is kept in ?image=<ref>.
 */
export default function PreparationSecurity() {
  const { prep, product } = usePreparationContext()
  const servings = useServings()
  const canEditOrder = useCan('orders:update')
  const [imageParam, setImageParam] = useQueryParam('image')
  const id = `${prep.namespace}/${prep.name}`
  const [loaded, setLoaded] = useState<{ id: string; report?: PreparationVulnerabilityReport; error?: string } | null>(null)

  // The images are fixed, but every scan of the Product writes a new report
  // (delivered via SSE), which can change the findings.
  const reports = (product?.vulnerabilities?.images ?? []).map((i) => i.report).join(',')
  useEffect(() => {
    let cancelled = false
    getPreparationVulnerabilities(prep.namespace, prep.name)
      .then((report) => {
        if (!cancelled) setLoaded({ id: `${prep.namespace}/${prep.name}`, report })
      })
      .catch((e: Error) => {
        if (!cancelled) setLoaded({ id: `${prep.namespace}/${prep.name}`, error: e.message })
      })
    return () => {
      cancelled = true
    }
  }, [prep.namespace, prep.name, reports])

  const current = loaded?.id === id ? loaded : null
  const report = current?.report
  const check = prep.checks?.vulnerabilities
  const images = (report?.images ?? []).map((image) => scannedImage(image, !!check))
  const selected = images.find((i) => i.key === imageParam) ?? images.find((i) => i.scan) ?? images[0]
  const deployed = servings?.find((s) => s.namespace === prep.namespace && s.order === prep.order)?.observedPreparation === prep.name

  return (
    <>
      {check ? (
        <Section title="Vulnerability check" description="Recorded when the Preparation was rendered; it holds new deployments only">
          <PropertyList
            items={[
              { label: 'Check', value: describeVulnerabilityCheck(check) },
              {
                label: 'Verdict',
                value: report
                  ? <VerdictLine verdict={checkVerdict(report)} />
                  : <span className={layout.muted}>{current?.error ? 'Unknown: the scans could not be read' : 'Loading…'}</span>,
              },
            ]}
          />
        </Section>
      ) : (
        <p className={layout.pageNote}>
          No vulnerability check: findings don't hold this Preparation's promotion.
          {canEditOrder && (
            <>
              {' '}
              <Link className={layout.inlineLink} to={paths.order(prep.namespace, prep.order, 'edit')}>Set one on the Order</Link>
            </>
          )}
        </p>
      )}

      {report?.comparison
        ? <ComparisonSection comparison={report.comparison} namespace={prep.namespace} />
        : deployed && <p className={layout.pageNote}>This Preparation is deployed.</p>}

      {current?.error ? (
        <Section>
          <EmptyState text={`Could not read the scans: ${current.error}`} />
        </Section>
      ) : (
        <>
          <Section
            title={<>Images<Counter count={images.length} /></>}
            description={report?.product && (
              <>
                Scanned as part of Product{' '}
                <Link className={layout.refLink} to={paths.product(prep.namespace, report.product, 'security')}>{report.product}</Link>
                {report.scanner && <> · <ScannerSummary scanner={report.scanner} /></>}
              </>
            )}
            flush
          >
            {!report ? (
              <EmptyState text="Loading…" />
            ) : images.length === 0 ? (
              <EmptyState text="The manifests reference no container images." />
            ) : (
              <ImageScanTable
                images={images}
                selected={selected?.key}
                selectLink={(image) => `?${new URLSearchParams({ image: image.key })}`}
                onSelect={(image) => setImageParam(image.key)}
                product={report.product ? { namespace: prep.namespace, name: report.product } : undefined}
                showBlocking={!!check}
              />
            )}
          </Section>

          <FindingsSection image={selected} loading={!report} />
        </>
      )}
    </>
  )
}
