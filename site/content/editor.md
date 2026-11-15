# Monaco editor

WebFile includes the Monaco editor for editing text, source code and configuration files in your browser. Changes are written to the server when you save.

## Open or create a file

Open a text file from the file view, or select it and choose **Open** from its actions. WebFile recognises common text and code extensions and checks the file's content type when needed.

To create a document, choose **New file**, enter the complete filename and select **Open editor**. Extensions are optional. The file is created on the server when you first save it.

The editor shows the filename and path at the top. The footer shows whether the document is saved or has unsaved changes.

## Edit and navigate

The compact toolbar provides:

| Control | What it does |
| --- | --- |
| Undo / Redo | Undo or reapply edits in the current document. |
| Find / Replace | Search within the document or replace matching text. |
| Word wrap | Wrap long lines to the editor width without changing the file's line breaks. |
| Show rich toolbar | Reveal additional display and editing controls. |
| Save | Write your changes to the server. You can also press Ctrl+S or Cmd+S. |

Select the language name in the footer to search for a language mode. WebFile chooses an initial mode from the filename; changing it controls syntax highlighting without renaming the file.

## Use the rich toolbar

Choose **Show rich toolbar** to access:

- **Minimap**, **whitespace** and **line numbers** for navigating and inspecting text.
- Font-size controls, including a reset button.
- **Format document** for JavaScript, TypeScript, JSON, HTML and CSS.
- **Markdown preview** when the language mode is Markdown.
- **Zen mode** to hide the surrounding editor controls, and **fullscreen** where the browser supports it.

Use **Exit Zen mode** or Escape to restore the controls. Formatting changes the document in the editor; save to write the result to the server.

## Preview Markdown

Open a Markdown file, enable the rich toolbar and choose **Show Markdown preview**. The rendered preview appears beside the source and updates as you type. Relative image paths are resolved from the document's folder.

Save writes the Markdown source, not the rendered preview. You can hide the preview to give the source more space.

## Save and handle conflicts

WebFile does not autosave. Choose **Save** or press Ctrl+S or Cmd+S, then check that the footer says **Saved**. Closing an editor with unsaved changes asks whether to discard them.

If the file has changed on the server since you opened it, or a new file now exists at the chosen name, WebFile shows **The target changed**. Your save has not overwritten the other file. Choose:

- **Cancel** to return to your edits and decide what to do.
- **Save as** with a different path to preserve both copies. The new path must not already exist.
- **Overwrite** to replace the current server file with your edited content.

Review the conflict before choosing Overwrite. Saving uses your Linux account's permissions; being able to open a file does not necessarily mean you can change it.

## Set your defaults

In **Settings → Editor**, choose defaults for word wrap, font size, line numbers, whitespace, minimap, toolbar layout and Markdown preview, then select **Save settings**. These defaults apply when you open an editor. Toolbar changes affect the current editor without saving new defaults.

The administrator controls the maximum editable file size through `editor_max_bytes`; the example configuration uses 5 MiB. Files above the limit cannot be opened or saved in the editor. See [configuration](configuration.md) for server settings.
