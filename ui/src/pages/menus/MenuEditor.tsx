import { useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import type { MenuFormData } from '../../api/types'
import { createMenu, updateMenu } from '../../api/client'
import { useMenus } from '../../hooks/useMenus'
import { useUnsavedChangesGuard } from '../../hooks/useUnsavedChangesGuard'
import PageHeader from '../../components/layout/PageHeader'
import ResourceNotFound from '../../components/layout/ResourceNotFound'
import MenuForm from '../../components/menu/MenuForm'
import { paths } from '../../routes/paths'
import layout from '../../components/layout/layout.module.css'

/** Full-page create (/menus/new) and edit (/menus/:ns/:name/edit) screen. */
export default function MenuEditor() {
  const { namespace, name } = useParams()
  const navigate = useNavigate()
  const menus = useMenus()
  const [dirty, setDirty] = useState(false)
  const { allowNavigation, dialog } = useUnsavedChangesGuard(dirty)

  const isEdit = !!name
  const menu = isEdit ? menus?.find((m) => m.namespace === namespace && m.name === name) : undefined

  if (isEdit && !menu) {
    return (
      <ResourceNotFound kind="Menu" listPath={paths.menus()} listLabel="Menus" namespace={namespace} name={name} loading={menus === null} />
    )
  }

  async function handleSubmit(data: MenuFormData) {
    if (menu) await updateMenu(menu.namespace, menu.name, data)
    else await createMenu(data)
    allowNavigation()
    navigate(paths.menu(data.namespace, data.name))
  }

  const back = menu ? paths.menu(menu.namespace, menu.name) : paths.menus()

  return (
    <div className={layout.page}>
      <PageHeader
        breadcrumbs={
          menu
            ? [{ label: 'Menus', to: paths.menus() }, { label: menu.namespace }, { label: menu.name, to: back }]
            : [{ label: 'Menus', to: paths.menus() }]
        }
        title={menu ? 'Edit Menu' : 'New Menu'}
        subtitle="Pin a source and decide what Orders created from this Menu may override."
      />
      <MenuForm
        key={menu ? `${menu.namespace}/${menu.name}` : 'new'}
        menu={menu}
        onSubmit={handleSubmit}
        onCancel={() => navigate(back)}
        onDirtyChange={setDirty}
      />
      {dialog}
    </div>
  )
}
