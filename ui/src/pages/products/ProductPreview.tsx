import { previewProduct } from '../../api/client'
import Section from '../../components/layout/Section'
import PreviewTab from '../../components/shared/PreviewTab'
import { useProductContext } from './productFormat'

export default function ProductPreview() {
  const { product } = useProductContext()

  return (
    <Section title="Preview" description="The Product rendered from its declared source, without any Order on top">
      <PreviewTab
        request={{
          namespace: product.namespace,
          name: product.name,
          source: product.source,
          render: product.render,
          patches: product.patches,
          images: product.images,
        }}
        render={previewProduct}
      />
    </Section>
  )
}
