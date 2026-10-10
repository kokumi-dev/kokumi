import { useNavigate } from 'react-router'
import { useOrders } from '../../hooks/useOrders'
import Section from '../../components/layout/Section'
import MenuRelationGraph from '../../components/menu/MenuRelationGraph'
import { paths } from '../../routes/paths'
import { useMenuContext } from './menuFormat'

export default function MenuOrders() {
  const { menu } = useMenuContext()
  const orders = useOrders()
  const navigate = useNavigate()

  return (
    <Section title="Orders" description="Orders created from this Menu. Select one to open it.">
      <MenuRelationGraph
        menu={menu}
        orders={orders}
        onSelectOrder={(o) => navigate(paths.order(o.namespace, o.name))}
      />
    </Section>
  )
}
