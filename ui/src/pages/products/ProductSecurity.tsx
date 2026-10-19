import { useEffect, useState } from 'react'
import { getProductVulnerabilities } from '../../api/client'
import type { ImageReport, ProductVulnerabilityReport } from '../../api/types'
import { imagePolicy } from '../../api/types'
import Section from '../../components/layout/Section'
import EmptyState from '../../components/layout/EmptyState'
import ScannerSummary from '../../components/vulnerability/ScannerSummary'
import ImageScanTable from '../../components/vulnerability/ImageScanTable'
import FindingsSection from '../../components/vulnerability/FindingsSection'
import { Counter, ShieldIcon } from '../../components/ui'
import { useQueryParam } from '../../hooks/useFilterParam'
import { stripOCIScheme } from '../../utils/format'
import { lastSegment, shortRef, type ScannedImage } from '../../utils/vulnerabilities'
import { useProductContext } from './productFormat'
import layout from '../../components/layout/layout.module.css'

/** An image of the Product as the table shows it: by the references the manifests use. */
function scannedImage(scan: ImageReport): ScannedImage {
  const repository = stripOCIScheme(scan.repository)
  const names = scan.refs?.length ? [...new Set(scan.refs.map(shortRef))] : [lastSegment(repository)]
  return {
    key: scan.digest,
    names,
    label: names.join(', '),
    title: `${(scan.refs?.length ? scan.refs : [repository]).join('\n')}\n${scan.digest}`,
    digest: scan.digest,
    scan,
    usedBy: scan.usedBy,
  }
}

/**
 * ProductSecurity shows the latest scan of each vendored image the
 * Product's Orders use, and the findings of the selected image's signed
 * report. The scans come with the Product; the findings are read from the
 * registry. The selection is kept in ?image=<digest>.
 */
export default function ProductSecurity() {
  const { product } = useProductContext()
  const id = `${product.namespace}/${product.name}`
  const recorded = product.vulnerabilities
  const [loaded, setLoaded] = useState<{ id: string; report?: ProductVulnerabilityReport; error?: string } | null>(null)
  const [imageParam, setImageParam] = useQueryParam('image')

  // Every scan writes a new report, so a changed list of reports (delivered
  // via SSE) reloads the findings.
  const reports = (recorded?.images ?? []).map((i) => i.report).join(',')
  useEffect(() => {
    if (!reports) return
    let cancelled = false
    getProductVulnerabilities(product.namespace, product.name)
      .then((report) => {
        if (!cancelled) setLoaded({ id: `${product.namespace}/${product.name}`, report })
      })
      .catch((e: Error) => {
        if (!cancelled) setLoaded({ id: `${product.namespace}/${product.name}`, error: e.message })
      })
    return () => {
      cancelled = true
    }
  }, [product.namespace, product.name, reports])

  const current = loaded?.id === id ? loaded : null
  const scanned = product.conditions?.find((c) => c.type === 'Scanned')
  // The record shows at once; the report adds the findings and read errors.
  const scans: ImageReport[] = reports ? current?.report?.images ?? recorded?.images ?? [] : []
  const images = scans.map(scannedImage)
  // By default the first image an Order deploys, whose findings matter most.
  const selected = images.find((i) => i.key === imageParam)
    ?? images.find((i) => i.usedBy?.some((u) => u.deployed))
    ?? images[0]
  const policy = imagePolicy(product.images?.policy)

  return (
    <>
      {scanned && scanned.status !== 'True' && (
        <div className={layout.notice} role="status">
          <strong>Scanned: {scanned.reason}</strong>
          {scanned.message && <span>{scanned.message}</span>}
        </div>
      )}

      {images.length === 0 ? (
        <Section>
          <EmptyState
            icon={<ShieldIcon size={40} className={layout.emptyIcon} />}
            text="No scans yet"
            action={
              <span className={layout.emptyHint}>
                {policy === 'Vendor'
                  ? 'Kokumi scans the images this Product vendors once scanning is enabled in the Kitchen (spec.scanning).'
                  : `Kokumi scans only the images it vendors, and this Product's image policy is ${policy}. Set it to Vendor, and enable scanning in the Kitchen (spec.scanning).`}
              </span>
            }
          />
        </Section>
      ) : (
        <>
          <Section
            title={<>Images<Counter count={images.length} /></>}
            description={recorded?.scanner
              ? <>The latest scan of each vendored image in use · <ScannerSummary scanner={recorded.scanner} /></>
              : 'The latest scan of each vendored image in use'}
            flush
          >
            <ImageScanTable
              images={images}
              selected={selected?.key}
              selectLink={(image) => `?${new URLSearchParams({ image: image.key })}`}
              onSelect={(image) => setImageParam(image.key)}
              product={product}
            />
          </Section>

          <FindingsSection image={selected} loading={!current} error={current?.error} />
        </>
      )}
    </>
  )
}
