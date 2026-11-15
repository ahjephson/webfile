# Transfers and tasks

Tasks shows file operations and their progress. Long-running work can continue while you browse elsewhere. Persistent operation history survives broker restarts; available pause and cancellation actions depend on the operation.

## Resumable uploads

Uploads use an 8 MiB chunked protocol. Selected files appear in Tasks before content transfer starts, and the current workspace sends them through a serial upload queue. Navigating to another folder does not change their destination.

Reselecting or retrying the same file can resume from the server's recorded offset using its destination, size and browser `lastModified` metadata. Cancelling discards staged data. Abandoned staging expires after seven days without activity; removing failed or interrupted upload history also discards its checkpoint.

## Undo the latest copy or move

The latest completed copy or move offers Undo in Tasks history for one hour. That deadline survives restarts. Transfers using Keep both are supported too.

A later accepted operation that changes user files clears the previous Undo set across all tabs. This includes delete, rename, uploads, editor saves, folder creation, permission changes and extraction. Failure or cancellation of the later operation does not restore the older set. Browsing, searching, previewing and downloading preserve it.

Copy Undo validates created entries before removing anything and retains changed or occupied content rather than blindly deleting it. A moved folder is moved back with its current contents, including later external edits. Read any reported conflicts; Undo is not a replacement for backups.

## Understand progress

Some tasks must count entries before a percentage is available. Delete first shows indeterminate progress, then completed/total items. Search results can be partial when a bound is reached. Cancellation is cooperative and may take time while an external tool or filesystem call returns.

Video thumbnail work has its own request-driven queue and appears in Tasks without becoming durable file-operation history. Ordinary image thumbnails use a separate fast path and stay out of Tasks.
