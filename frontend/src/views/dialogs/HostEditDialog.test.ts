// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Vitest tests for HostEditDialog. The test scope
// is smoke + key flows (mount, render, submit
// success, submit error, watcher reset) per the
// PR #270 brief. Edge cases (e.g. host type
// variants, the endpoint add/remove flow, every
// Zod superRefine rule) are covered by the parent
// view's manual QA.

// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'

import HostEditDialog from './HostEditDialog.vue'
import type { Host, Inbound, Node } from '@/types'
import { updateHost } from '@/api/services'
import { makeI18n, uiStubs } from './__test-helpers'

vi.mock('@/api/services', () => ({
  updateHost: vi.fn(),
}))

const testNode: Node = {
  id: '550e8400-e29b-41d4-a716-446655440000',
  name: 'test-node',
  region: 'us-east',
  state: 'online',
  address: '10.0.0.1:22',
  capacityHint: '100',
  createdAt: '2026-08-01T10:00:00Z',
  updatedAt: '2026-08-01T10:00:00Z',
}

const testInbound: Inbound = {
  id: '660e8400-e29b-41d4-a716-446655440000',
  nodeId: testNode.id,
  name: 'vl-443',
  protocol: 'vless',
  listen: '0.0.0.0',
  listenPort: 443,
  enabled: true,
  createdAt: '2026-08-01T10:00:00Z',
  updatedAt: '2026-08-01T10:00:00Z',
}

const testHost: Host = {
  id: '770e8400-e29b-41d4-a716-446655440000',
  remark: 'prod-edge',
  displayName: 'Production Edge',
  type: 'direct',
  enabled: true,
  priority: 50,
  country: 'NL',
  city: 'Amsterdam',
  endpoints: [
    {
      nodeId: testNode.id,
      inboundId: testInbound.id,
      protocol: 'vless',
      weight: 1,
    },
  ],
  createdAt: '2026-08-01T10:00:00Z',
  updatedAt: '2026-08-01T10:00:00Z',
}

const loadInbounds = vi.fn(async (_nodeId: string) => [testInbound])

describe('HostEditDialog', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.resetAllMocks()
    loadInbounds.mockClear()
  })

  it('mounts without crashing when closed', () => {
    const wrapper = mount(HostEditDialog, {
      props: {
        open: false,
        host: null,
        nodes: [testNode],
        inboundsByNode: { [testNode.id]: [testInbound] },
        loadInboundsForNode: loadInbounds,
      },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    expect(wrapper.exists()).toBe(true)
  })

  it('renders the form fields when open with a valid host', async () => {
    const wrapper = mount(HostEditDialog, {
      props: {
        open: true,
        host: testHost,
        nodes: [testNode],
        inboundsByNode: { [testNode.id]: [testInbound] },
        loadInboundsForNode: loadInbounds,
      },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    expect(wrapper.text()).toContain('hosts.remark')
    expect(wrapper.text()).toContain('hosts.displayName')
    expect(wrapper.text()).toContain('hosts.type')
    expect(wrapper.text()).toContain('hosts.priority')
    expect(wrapper.text()).toContain('hosts.country')
    expect(wrapper.text()).toContain('hosts.city')
  })

  it('hydrates the remark field from the host prop on open', async () => {
    const wrapper = mount(HostEditDialog, {
      props: {
        open: true,
        host: testHost,
        nodes: [testNode],
        inboundsByNode: { [testNode.id]: [testInbound] },
        loadInboundsForNode: loadInbounds,
      },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    const remarkInput = wrapper
      .findAll('input')
      .find((i) => i.element.value === testHost.remark)
    expect(remarkInput).toBeDefined()
  })

  it('resets the form when the host prop changes while open', async () => {
    const otherHost: Host = {
      ...testHost,
      id: '880e8400-e29b-41d4-a716-446655440000',
      remark: 'staging-edge',
    }
    const wrapper = mount(HostEditDialog, {
      props: {
        open: true,
        host: testHost,
        nodes: [testNode],
        inboundsByNode: { [testNode.id]: [testInbound] },
        loadInboundsForNode: loadInbounds,
      },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await wrapper.setProps({ host: otherHost })
    await nextTick()
    const remarkInput = wrapper
      .findAll('input')
      .find((i) => i.element.value === otherHost.remark)
    expect(remarkInput).toBeDefined()
  })

  it('calls updateHost with the host id on submit', async () => {
    vi.mocked(updateHost).mockResolvedValue(testHost)
    const wrapper = mount(HostEditDialog, {
      props: {
        open: true,
        host: testHost,
        nodes: [testNode],
        inboundsByNode: { [testNode.id]: [testInbound] },
        loadInboundsForNode: loadInbounds,
      },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await flushPromises()
    const formEl = wrapper.find('form').element as HTMLFormElement
    formEl.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await vi.waitFor(
      () => {
        expect(updateHost).toHaveBeenCalled()
      },
      { timeout: 1000 },
    )
    const callArgs = vi.mocked(updateHost).mock.calls[0]
    expect(callArgs[0]).toBe(testHost.id)
  })

  it('emits updated on submit success', async () => {
    vi.mocked(updateHost).mockResolvedValue(testHost)
    const wrapper = mount(HostEditDialog, {
      props: {
        open: true,
        host: testHost,
        nodes: [testNode],
        inboundsByNode: { [testNode.id]: [testInbound] },
        loadInboundsForNode: loadInbounds,
      },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await flushPromises()
    const formEl = wrapper.find('form').element as HTMLFormElement
    formEl.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await vi.waitFor(
      () => {
        expect(wrapper.emitted('updated')).toBeDefined()
      },
      { timeout: 1000 },
    )
    expect(wrapper.emitted('updated')?.[0]?.[0]).toEqual(testHost)
  })

  it('does not emit updated on submit error', async () => {
    vi.mocked(updateHost).mockRejectedValue(new Error('boom'))
    const wrapper = mount(HostEditDialog, {
      props: {
        open: true,
        host: testHost,
        nodes: [testNode],
        inboundsByNode: { [testNode.id]: [testInbound] },
        loadInboundsForNode: loadInbounds,
      },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await flushPromises()
    const formEl = wrapper.find('form').element as HTMLFormElement
    formEl.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await vi.waitFor(
      () => {
        expect(updateHost).toHaveBeenCalled()
      },
      { timeout: 1000 },
    )
    expect(wrapper.emitted('updated')).toBeUndefined()
  })

  // v0.8.32.2 (#304) regression guard: the edit
  // dialog's submit must preserve every endpoint
  // field that the dialog does not display. Pre-fix,
  // the form's PUT payload only carried 5 fields
  // (nodeId, inboundId, weight, address, port) and
  // the backend's wholesale-replace semantic wiped
  // every other field on every save. This test
  // submits a host whose endpoint carries the full
  // v0.7.x shape (sni, host, path, downloadHostId,
  // protocol, id) and asserts the updateHost call's
  // payload retains every one of those keys, even
  // though the dialog only shows the 5 editable
  // fields.
  it('preserves non-editable endpoint fields (sni, host, path, downloadHostId, protocol, id) across save (#304)', async () => {
    const fullEndpointId = '880e8400-e29b-41d4-a716-446655440099'
    const downloadHostId = '880e8400-e29b-41d4-a716-446655440123'
    const hostWithFullEndpoint: Host = {
      ...testHost,
      endpoints: [
        {
          id: fullEndpointId,
          nodeId: testNode.id,
          inboundId: testInbound.id,
          protocol: 'vless',
          weight: 1,
          // The non-editable fields the dialog never
          // shows but the backend stores per endpoint.
          sni: ['cdn.example.com', 'cdn2.example.com'],
          host: ['example.com'],
          path: '/secretpath',
          downloadHostId,
        },
      ],
    }
    vi.mocked(updateHost).mockResolvedValue(hostWithFullEndpoint)
    const wrapper = mount(HostEditDialog, {
      props: {
        open: true,
        host: hostWithFullEndpoint,
        nodes: [testNode],
        inboundsByNode: { [testNode.id]: [testInbound] },
        loadInboundsForNode: loadInbounds,
      },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await flushPromises()
    const formEl = wrapper.find('form').element as HTMLFormElement
    formEl.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await vi.waitFor(
      () => {
        expect(updateHost).toHaveBeenCalled()
      },
      { timeout: 1000 },
    )
    const payload = vi.mocked(updateHost).mock.calls[0]?.[1] as {
      endpoints?: Array<{
        nodeId: string
        inboundId: string
        weight: number
        protocol?: string
        id?: string
        sni?: string[]
        host?: string[]
        path?: string
        downloadHostId?: string
      }>
    }
    expect(payload.endpoints).toBeDefined()
    expect(payload.endpoints).toHaveLength(1)
    const ep = payload.endpoints![0]
    // The 5 editable fields come from the row.
    expect(ep.nodeId).toBe(testNode.id)
    expect(ep.inboundId).toBe(testInbound.id)
    expect(ep.weight).toBe(1)
    // The non-editable fields survive the round-trip
    // — this is the v0.8.32.2 fix. Pre-fix, all 5
    // of the lines below were `undefined`.
    expect(ep.id).toBe(fullEndpointId)
    expect(ep.protocol).toBe('vless')
    expect(ep.sni).toEqual(['cdn.example.com', 'cdn2.example.com'])
    expect(ep.host).toEqual(['example.com'])
    expect(ep.path).toBe('/secretpath')
    expect(ep.downloadHostId).toBe(downloadHostId)
  })
})
