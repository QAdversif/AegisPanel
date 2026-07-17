<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  AppLayout. The single top-level layout for the
  Aegis admin UI. Three regions:
    * Sidebar — primary navigation
    * Topbar  — brand + theme toggle + user menu
    * Main    — the routed view

  Per ADR-0004 the look is a slate base, dark by
  default, dev-tool aesthetic. The sidebar is
  always-visible on desktop and collapses into a
  Sheet (drawer) on mobile (the breakpoint is set
  by the `md:` Tailwind prefix at 768px).

  v0.1.0 ships the layout shell + nav stub. The
  user menu shows the panel reachability status
  (from the auth store); real logout / profile
  links land with the auth module in Phase 1+.
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { LayoutDashboard, Menu, Moon, Server, Settings, Shield, Sun, Users, Wifi } from 'lucide-vue-next'

import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'

import Button from '@/components/ui/Button.vue'
import Badge from '@/components/ui/Badge.vue'
import TooltipProvider from '@/components/ui/TooltipProvider.vue'
import DropdownMenu from '@/components/ui/DropdownMenu.vue'
import DropdownMenuTrigger from '@/components/ui/DropdownMenuTrigger.vue'
import DropdownMenuContent from '@/components/ui/DropdownMenuContent.vue'
import DropdownMenuItem from '@/components/ui/DropdownMenuItem.vue'
import DropdownMenuSeparator from '@/components/ui/DropdownMenuSeparator.vue'
import DropdownMenuLabel from '@/components/ui/DropdownMenuLabel.vue'
import Sheet from '@/components/ui/Sheet.vue'
import Toaster from '@/components/ui/Toaster.vue'

const { t } = useI18n()
const route = useRoute()
const auth = useAuthStore()
const ui = useUiStore()

const mobileNavOpen = ref(false)

// Nav entries. v0.1.0 has only Dashboard; the
// rest are placeholders that will be wired in
// PR-D. Keeping them visible (greyed) makes the
// roadmap legible from the chrome alone.
interface NavItem {
  key: string
  to: string
  label: string
  icon: typeof LayoutDashboard
  enabled: boolean
}

const navItems: NavItem[] = [
  { key: 'dashboard', to: '/', label: t('nav.dashboard'), icon: LayoutDashboard, enabled: true },
  { key: 'nodes', to: '/nodes', label: t('nav.nodes'), icon: Server, enabled: false },
  { key: 'inbounds', to: '/inbounds', label: t('nav.inbounds'), icon: Shield, enabled: false },
  { key: 'hosts', to: '/hosts', label: t('nav.hosts'), icon: Wifi, enabled: false },
  { key: 'users', to: '/users', label: t('nav.users'), icon: Users, enabled: false },
  { key: 'settings', to: '/settings', label: t('nav.settings'), icon: Settings, enabled: false },
]

const statusLabel = computed(() => t(`dashboard.status.${auth.status}`))
const statusVariant = computed(() => {
  if (auth.status === 'ok') return 'success'
  if (auth.status === 'down') return 'destructive'
  return 'warning'
})
</script>

<template>
  <TooltipProvider>
    <div class="aegis-layout">
      <!-- Sidebar (desktop) -->
      <aside class="aegis-layout__sidebar">
        <div class="aegis-layout__brand">
          <span
            class="aegis-layout__logo"
            aria-hidden="true"
          >⛨</span>
          <span class="aegis-layout__title">Aegis</span>
        </div>

        <nav
          class="aegis-layout__nav"
          aria-label="Primary"
        >
          <RouterLink
            v-for="item in navItems"
            :key="item.key"
            :to="item.to"
            :class="[
              'aegis-layout__nav-item',
              !item.enabled && 'aegis-layout__nav-item--disabled',
              route.path === item.to && item.enabled && 'aegis-layout__nav-item--active',
            ]"
            :aria-disabled="!item.enabled || undefined"
            :tabindex="!item.enabled ? -1 : undefined"
            @click.capture="!item.enabled && $event.preventDefault()"
          >
            <component
              :is="item.icon"
              class="h-4 w-4"
            />
            <span>{{ item.label }}</span>
            <Badge
              v-if="!item.enabled"
              variant="outline"
              class="ml-auto text-[10px]"
            >
              {{ t('nav.soon') }}
            </Badge>
          </RouterLink>
        </nav>

        <div class="aegis-layout__sidebar-footer">
          <small>v0.0.0-dev</small>
        </div>
      </aside>

      <!-- Right column: topbar + main -->
      <div class="aegis-layout__column">
        <header class="aegis-layout__topbar">
          <div class="aegis-layout__topbar-left">
            <Sheet
              v-model:open="mobileNavOpen"
              side="left"
              content-class="w-64 p-0"
            >
              <template #trigger>
                <Button
                  variant="ghost"
                  size="icon"
                  class="md:hidden"
                  :aria-label="t('nav.openMenu')"
                  @click="mobileNavOpen = true"
                >
                  <Menu class="h-5 w-5" />
                </Button>
              </template>
              <nav
                class="aegis-layout__mobile-nav"
                aria-label="Primary mobile"
              >
                <RouterLink
                  v-for="item in navItems"
                  :key="item.key"
                  :to="item.to"
                  class="aegis-layout__nav-item"
                  @click="mobileNavOpen = false"
                >
                  <component
                    :is="item.icon"
                    class="h-4 w-4"
                  />
                  <span>{{ item.label }}</span>
                </RouterLink>
              </nav>
            </Sheet>
            <h1 class="aegis-layout__page-title">
              {{ t(`nav.${navItems.find((i) => i.to === route.path)?.key ?? 'dashboard'}`) }}
            </h1>
          </div>

          <div class="aegis-layout__topbar-right">
            <Button
              variant="ghost"
              size="icon"
              :aria-label="ui.theme === 'dark' ? t('topbar.themeLight') : t('topbar.themeDark')"
              @click="ui.toggleTheme()"
            >
              <Sun
                v-if="ui.theme === 'dark'"
                class="h-4 w-4"
              />
              <Moon
                v-else
                class="h-4 w-4"
              />
            </Button>

            <DropdownMenu>
              <DropdownMenuTrigger>
                <Button
                  variant="outline"
                  size="sm"
                  class="gap-2"
                >
                  <Badge :variant="statusVariant">
                    {{ statusLabel }}
                  </Badge>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent
                align="end"
                :side-offset="6"
              >
                <DropdownMenuLabel>{{ t('topbar.panelStatus') }}</DropdownMenuLabel>
                <DropdownMenuItem disabled>
                  {{ t('topbar.profileSoon') }}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem disabled>
                  {{ t('topbar.logoutSoon') }}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </header>

        <main class="aegis-layout__main">
          <RouterView v-slot="{ Component }">
            <component :is="Component" />
          </RouterView>
        </main>
      </div>

      <!-- Global toaster (mount once) -->
      <Toaster />
    </div>
  </TooltipProvider>
</template>

<style scoped>
.aegis-layout {
  display: grid;
  grid-template-columns: 240px 1fr;
  min-height: 100vh;
  background: hsl(var(--background));
  color: hsl(var(--foreground));
}

@media (max-width: 767px) {
  .aegis-layout {
    grid-template-columns: 1fr;
  }
}

.aegis-layout__sidebar {
  display: flex;
  flex-direction: column;
  border-right: 1px solid hsl(var(--border));
  background: hsl(var(--card));
  padding: 1rem;
}

@media (max-width: 767px) {
  .aegis-layout__sidebar {
    display: none;
  }
}

.aegis-layout__brand {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-weight: 600;
  letter-spacing: 0.05em;
  padding: 0.25rem 0.5rem 1rem;
}

.aegis-layout__logo {
  font-size: 1.5rem;
  color: hsl(var(--primary));
}

.aegis-layout__title {
  font-size: 1rem;
}

.aegis-layout__nav,
.aegis-layout__mobile-nav {
  display: flex;
  flex-direction: column;
  gap: 0.125rem;
}

.aegis-layout__mobile-nav {
  margin-top: 0.5rem;
}

.aegis-layout__nav-item {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.5rem 0.75rem;
  border-radius: 0.375rem;
  font-size: 0.875rem;
  color: hsl(var(--muted-foreground));
  text-decoration: none;
  transition: background-color 150ms, color 150ms;
}

.aegis-layout__nav-item:hover:not(.aegis-layout__nav-item--disabled):not(.aegis-layout__nav-item--active) {
  background: hsl(var(--accent));
  color: hsl(var(--accent-foreground));
}

.aegis-layout__nav-item--active {
  background: hsl(var(--accent));
  color: hsl(var(--accent-foreground));
  font-weight: 500;
}

.aegis-layout__nav-item--disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.aegis-layout__sidebar-footer {
  margin-top: auto;
  padding-top: 1rem;
  color: hsl(var(--muted-foreground));
  font-size: 0.75rem;
}

.aegis-layout__column {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.aegis-layout__topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.5rem 1rem;
  border-bottom: 1px solid hsl(var(--border));
  background: hsl(var(--card));
  height: 3.5rem;
  position: sticky;
  top: 0;
  z-index: 10;
}

.aegis-layout__topbar-left {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.aegis-layout__page-title {
  margin: 0;
  font-size: 1rem;
  font-weight: 600;
}

.aegis-layout__topbar-right {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.aegis-layout__main {
  flex: 1;
  padding: 1.5rem;
  overflow-y: auto;
}
</style>
