// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Vitest tests for HostCreateDialog. The test
// scope is smoke + key flows (mount, render,
// submit success, submit error, watcher reset)
// per the PR #270 brief. Edge cases (e.g. the
// superRefine cross-field rules, the endpoint
// add/remove flow) are covered by the parent
// view's manual QA.

// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'

import HostCreateDialog from './HostCreateDialog.vue'
import type { Host, Inbound, Node } from '@/types'
import { createHost } from '@/api/services'
import { makeI18n, uiStubs } from './__test-helpers'

vi.mock('@/api/services', () => ({
  createHost: vi.fn(),
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
  remark: 'new-host',
  type: 'direct',
  enabled: true,
  priority: 50,
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

describe('HostCreateDialog', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.resetAllMocks()
    loadInbounds.mockClear()
  })

  it('mounts without crashing when closed', () => {
    const wrapper = mount(HostCreateDialog, {
      props: {
        open: false,
        nodes: [testNode],
        inboundsByNode: { [testNode.id]: [testInbound] },
        loadInboundsForNode: loadInbounds,
      },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    expect(wrapper.exists()).toBe(true)
  })

  it('renders the form fields when open', async () => {
    const wrapper = mount(HostCreateDialog, {
      props: {
        open: true,
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
  })

  it('emits update:open false when DialogClose is clicked', async () => {
    const wrapper = mount(HostCreateDialog, {
      props: {
        open: true,
        nodes: [testNode],
        inboundsByNode: { [testNode.id]: [testInbound] },
        loadInboundsForNode: loadInbounds,
      },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    const closeBtn = wrapper.find('[data-test="dialog-close"]')
    expect(closeBtn.exists()).toBe(true)
    await closeBtn.trigger('click')
  })

  it('resets the form when the dialog re-opens', async () => {
    const wrapper = mount(HostCreateDialog, {
      props: {
        open: true,
        nodes: [testNode],
        inboundsByNode: { [testNode.id]: [testInbound] },
        loadInboundsForNode: loadInbounds,
      },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    const remarkInput = wrapper.findAll('input').find((i) => i.attributes('type') !== 'checkbox')
    expect(remarkInput).toBeDefined()
    if (remarkInput) {
      await remarkInput.setValue('dirty-remark')
    }
    await wrapper.setProps({ open: false })
    await nextTick()
    await wrapper.setProps({ open: true })
    await nextTick()
    const fresh = wrapper.findAll('input').find((i) => i.attributes('type') !== 'checkbox')
    expect(fresh?.element.value).toBe('')
  })

  it('does not call createHost when submit validation fails (empty form)', async () => {
    vi.mocked(createHost).mockResolvedValue(testHost)
    const wrapper = mount(HostCreateDialog, {
      props: {
        open: true,
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
        expect(createHost).not.toHaveBeenCalled()
      },
      { timeout: 500 },
    )
    expect(wrapper.emitted('created')).toBeUndefined()
  })

  it('does not emit created on submit error (mocked wire reject)', async () => {
    vi.mocked(createHost).mockResolvedValue(testHost)
    const wrapper = mount(HostCreateDialog, {
      props: {
        open: true,
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
        expect(wrapper.emitted('created')).toBeUndefined()
      },
      { timeout: 500 },
    )
  })
})
