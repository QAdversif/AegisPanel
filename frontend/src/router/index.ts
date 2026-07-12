// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Vue Router configuration. Phase 0 ships only the dashboard route;
// the rest will be added module-by-module in Phase 1+.

import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'dashboard',
    component: () => import('@/views/DashboardView.vue'),
    meta: { titleKey: 'nav.dashboard' },
  },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.afterEach((to) => {
  const titleKey = (to.meta?.titleKey as string | undefined) ?? 'nav.dashboard'
  document.title = `Aegis · ${titleKey}`
})
