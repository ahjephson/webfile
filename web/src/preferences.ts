import type { Preferences } from '@/types/files'

export type UserPreferences = Required<Preferences>

export const defaultPreferences: UserPreferences = {
  sort_field: 'name',
  sort_direction: 'asc',
  view_mode: 'list',
  grid_size: 'medium',
  show_hidden: false,
  hide_inaccessible: false,
  theme: 'system',
  editor_word_wrap: false,
  editor_font_size: 14,
  editor_line_numbers: true,
  editor_show_whitespace: false,
  editor_minimap: false,
  editor_toolbar: 'compact',
  editor_markdown_preview: false,
  copy_chunk_mib: 4,
  extract_existing_files: 'skip',
}

export function normalizePreferences(value: Preferences = {}): UserPreferences {
  const fontSize = Number.isInteger(value.editor_font_size) && value.editor_font_size! >= 10 && value.editor_font_size! <= 28
    ? value.editor_font_size!
    : defaultPreferences.editor_font_size
  return {
    sort_field: ['modified', 'size', 'type'].includes(value.sort_field ?? '') ? value.sort_field! : 'name',
    sort_direction: value.sort_direction === 'desc' ? 'desc' : 'asc',
    view_mode: value.view_mode === 'grid' ? 'grid' : 'list',
    grid_size: value.grid_size === 'small' || value.grid_size === 'large' ? value.grid_size : 'medium',
    show_hidden: value.show_hidden === true,
    hide_inaccessible: value.hide_inaccessible === true,
    theme: value.theme === 'dark' || value.theme === 'light' ? value.theme : 'system',
    editor_word_wrap: value.editor_word_wrap === true,
    editor_font_size: fontSize,
    editor_line_numbers: value.editor_line_numbers !== false,
    editor_show_whitespace: value.editor_show_whitespace === true,
    editor_minimap: value.editor_minimap === true,
    editor_toolbar: value.editor_toolbar === 'rich' ? 'rich' : 'compact',
    editor_markdown_preview: value.editor_markdown_preview === true,
    copy_chunk_mib: value.copy_chunk_mib === 1 || value.copy_chunk_mib === 16 || value.copy_chunk_mib === 64 ? value.copy_chunk_mib : 4,
    extract_existing_files: value.extract_existing_files === 'overwrite' ? 'overwrite' : 'skip',
  }
}
