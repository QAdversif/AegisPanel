// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Vitest tests for NodeEditDialog. The test scope
// is smoke + key flows (mount, render, submit
// success, submit error, watcher reset) per the
// PR #270 brief. Edge cases (e.g. every Zod
// validation rule, every FormField error state)
// are covered by the parent view's manual QA.

// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'

import NodeEditDialog from './NodeEditDialog.vue'
import type { Node } from '@/types'
import { updateNode } from '@/api/services'
import { makeI18n, uiStubs } from './__test-helpers'

vi.mock('@/api/services', () => ({
  updateNode: vi.fn(),
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

describe('NodeEditDialog', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.resetAllMocks()
  })

  it('mounts without crashing when closed', () => {
    const wrapper = mount(NodeEditDialog, {
      props: { open: false, node: null },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    expect(wrapper.exists()).toBe(true)
  })

  it('renders the form fields when open with a valid node', () => {
    const wrapper = mount(NodeEditDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    expect(wrapper.text()).toContain('nodes.name')
    expect(wrapper.text()).toContain('nodes.region')
    expect(wrapper.text()).toContain('nodes.address')
    expect(wrapper.text()).toContain('nodes.capacityHint')
  })

  it('hydrates form fields from the node prop on open', async () => {
    const wrapper = mount(NodeEditDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    const inputs = wrapper.findAll('input')
    const nameInput = inputs.find((i) => i.element.value === testNode.name)
    expect(nameInput).toBeDefined()
    const regionInput = inputs.find((i) => i.element.value === testNode.region)
    expect(regionInput).toBeDefined()
  })

  it('emits update:open false when DialogClose is clicked', async () => {
    const wrapper = mount(NodeEditDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    const closeBtn = wrapper.find('[data-test="dialog-close"]')
    expect(closeBtn.exists()).toBe(true)
    await closeBtn.trigger('click')
  })

  it('resets form fields when node prop changes while open', async () => {
    const otherNode: Node = {
      ...testNode,
      id: '660e8400-e29b-41d4-a716-446655440001',
      name: 'other-node',
      region: 'eu-west',
    }
    const wrapper = mount(NodeEditDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await wrapper.setProps({ node: otherNode })
    await nextTick()
    const inputs = wrapper.findAll('input')
    const nameInput = inputs.find((i) => i.element.value === otherNode.name)
    expect(nameInput).toBeDefined()
    const staleInput = inputs.find((i) => i.element.value === testNode.name)
    expect(staleInput).toBeUndefined()
  })

  it('calls updateNode with the node id on submit', async () => {
    vi.mocked(updateNode).mockResolvedValue(testNode)
    const wrapper = mount(NodeEditDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await flushPromises()
    const formEl = wrapper.find('form').element as HTMLFormElement
    formEl.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await vi.waitFor(
      () => {
        expect(updateNode).toHaveBeenCalled()
      },
      { timeout: 1000 },
    )
    const callArgs = vi.mocked(updateNode).mock.calls[0]
    expect(callArgs[0]).toBe(testNode.id)
  })

  it('emits updated on submit success', async () => {
    vi.mocked(updateNode).mockResolvedValue(testNode)
    const wrapper = mount(NodeEditDialog, {
      props: { open: true, node: testNode },
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
    expect(wrapper.emitted('updated')?.[0]?.[0]).toEqual(testNode)
  })

  it('does not emit updated on submit error', async () => {
    vi.mocked(updateNode).mockRejectedValue(new Error('boom'))
    const wrapper = mount(NodeEditDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await flushPromises()
    const formEl = wrapper.find('form').element as HTMLFormElement
    formEl.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await vi.waitFor(
      () => {
        expect(updateNode).toHaveBeenCalled()
      },
      { timeout: 1000 },
    )
    expect(wrapper.emitted('updated')).toBeUndefined()
  })
})
