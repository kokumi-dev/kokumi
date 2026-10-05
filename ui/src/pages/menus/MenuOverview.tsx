import Section from '../../components/layout/Section'
import PropertyList from '../../components/layout/PropertyList'
import ConditionList from '../../components/layout/ConditionList'
import PatchList from '../../components/shared/PatchList'
import { formatDate } from '../../utils/format'
import { useMenuContext } from './menuFormat'
import layout from '../../components/layout/layout.module.css'

export default function MenuOverview() {
  const { menu } = useMenuContext()
  const { values, patches } = menu.overrides

  return (
    <>
      <Section title="Source">
        <PropertyList
          items={[
            {
              label: 'Source',
              value: <span className={layout.mono}>{menu.source.pantryRef?.name ? `pantry: ${menu.source.pantryRef.name}` : menu.source.oci}</span>,
            },
            { label: 'Version', value: menu.source.version },
            { label: 'Renderer', value: menu.render?.helm ? 'Helm' : 'Manifest', show: !!menu.render },
            { label: 'Default promotion', value: menu.defaults.mode },
            {
              label: 'Vendoring',
              value: menu.vendor && (
                <span className={layout.mono}>
                  {menu.vendor.mode ?? 'Render'} → {menu.vendor.destination.oci || (menu.vendor.destination.pantryRef?.name ? `pantry: ${menu.vendor.destination.pantryRef.name}` : 'in-cluster registry')}
                </span>
              ),
              show: !!menu.vendor,
            },
            { label: 'Created', value: formatDate(menu.createdAt) },
          ]}
        />
      </Section>

      <Section title="Override policy" description="What Orders created from this Menu may change">
        <PropertyList
          items={[
            { label: 'Values', value: values.policy },
            { label: 'Allowed values', value: values.allowed?.join(', '), show: values.policy === 'Restricted' && !!values.allowed?.length },
            { label: 'Patches', value: patches.policy },
            {
              label: 'Allowed patches',
              value: (
                <span className={layout.mono}>
                  {patches.allowed?.map((a) => `${a.target.kind}/${a.target.name}: ${a.paths.join(', ')}`).join('; ')}
                </span>
              ),
              show: patches.policy === 'Restricted' && !!patches.allowed?.length,
            },
          ]}
        />
      </Section>

      {menu.patches && menu.patches.length > 0 && (
        <Section title={`Base patches (${menu.patches.length})`} description="Applied to every Order created from this Menu">
          <PatchList patches={menu.patches} />
        </Section>
      )}

      <Section title="Conditions">
        <ConditionList conditions={menu.conditions} />
      </Section>
    </>
  )
}
