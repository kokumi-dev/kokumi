import { useMemo } from 'react'
import { useOrders } from '../../hooks/useOrders'
import { usePreparations } from '../../hooks/usePreparations'
import { useServings } from '../../hooks/useServings'
import { useDeniedReason } from '../../hooks/useCapability'
import Section from '../../components/layout/Section'
import { ordersByProduct, productDeletingReason, productKey } from '../../utils/products'
import OrderGraph from './OrderGraph'
import { useProductContext } from './productFormat'

export default function ProductOrders() {
  const { product } = useProductContext()
  const orders = useOrders()
  const preparations = usePreparations()
  const servings = useServings()
  const orderDenied = useDeniedReason('orders:create')
  const variants = useMemo(
    () => (orders === null ? null : ordersByProduct(orders).get(productKey(product.namespace, product.name)) ?? []),
    [orders, product.namespace, product.name],
  )

  return (
    <Section title="Orders" description="The variants of this Product. Select one to see what it runs.">
      <OrderGraph
        product={product}
        orders={variants}
        preparations={preparations}
        servings={servings}
        orderDenied={productDeletingReason(product) ?? orderDenied}
      />
    </Section>
  )
}
