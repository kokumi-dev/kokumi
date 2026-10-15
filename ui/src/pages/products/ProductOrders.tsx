import { useNavigate } from 'react-router'
import { useOrders } from '../../hooks/useOrders'
import Section from '../../components/layout/Section'
import ProductRelationGraph from '../../components/product/ProductRelationGraph'
import { paths } from '../../routes/paths'
import { useProductContext } from './productFormat'

export default function ProductOrders() {
  const { product } = useProductContext()
  const orders = useOrders()
  const navigate = useNavigate()

  return (
    <Section title="Orders" description="The variants of this Product. Select one to open it.">
      <ProductRelationGraph
        product={product}
        orders={orders}
        onSelectOrder={(o) => navigate(paths.order(o.namespace, o.name))}
      />
    </Section>
  )
}
