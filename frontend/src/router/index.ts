// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Vue Router configuration. Phase 0 ships only the
// dashboard route; Phase 1 (PR-D) adds the rest of
// the v0.1.0 CRUD surface.
//
// Auth gate: every route except /login requires an
// authenticated session. The `meta.requiresAuth`
// flag drives the check in the beforeEach guard
// below. Unauthenticated users are redirected to
// /login with the original URL in the `redirect`
// query param so the login view can return them
// where they came from.

import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

import { useAuthStore } from '@/stores/auth'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { titleKey: 'login.title', requiresAuth: false, layout: 'auth' },
  },
  {
    path: '/',
    name: 'dashboard',
    component: () => import('@/views/DashboardView.vue'),
    meta: { titleKey: 'nav.dashboard', requiresAuth: true, layout: 'app' },
  },
  {
    path: '/nodes',
    name: 'nodes',
    component: () => import('@/views/NodesView.vue'),
    meta: { titleKey: 'nav.nodes', requiresAuth: true, layout: 'app' },
  },
  {
    path: '/inbounds',
    name: 'inbounds',
    component: () => import('@/views/InboundsView.vue'),
    meta: { titleKey: 'nav.inbounds', requiresAuth: true, layout: 'app' },
  },
  {
    path: '/hosts',
    name: 'hosts',
    component: () => import('@/views/HostsView.vue'),
    meta: { titleKey: 'nav.hosts', requiresAuth: true, layout: 'app' },
  },
  {
    path: '/users',
    name: 'users',
    component: () => import('@/views/UsersView.vue'),
    meta: { titleKey: 'nav.users', requiresAuth: true, layout: 'app' },
  },
  {
    path: '/subscription',
    name: 'subscription',
    component: () => import('@/views/SubscriptionView.vue'),
    meta: { titleKey: 'nav.subscription', requiresAuth: true, layout: 'app' },
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('@/views/SettingsView.vue'),
    meta: { titleKey: 'nav.settings', requiresAuth: true, layout: 'app' },
  },
  {
    path: '/:pathMatch(.*)*',
    redirect: '/',
  },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach((to) => {
  const auth = useAuthStore()
  const requiresAuth = to.meta?.requiresAuth !== false

  if (requiresAuth && !auth.isAuthenticated) {
    return {
      name: 'login',
      query: { redirect: to.fullPath },
    }
  }

  if (to.name === 'login' && auth.isAuthenticated) {
    return { path: '/' }
  }

  return true
})

router.afterEach((to) => {
  const titleKey = (to.meta?.titleKey as string | undefined) ?? 'nav.dashboard'
  document.title = `Aegis · ${titleKey}`
})
