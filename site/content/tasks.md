# Transfers and tasks

Tasks shows file operations and their progress. Long-running work can continue while you browse elsewhere. Persistent operation history survives broker restarts; available pause and cancellation actions depend on the operation.

## Resumable uploads

Selected uploads appear in Tasks before their content starts transferring. Files upload one at a time, and navigating to another folder does not change their destination.

To resume an interrupted upload, select the same file again at the same destination. Cancelling discards the partial upload. Partial uploads also expire after seven days without activity; removing failed or interrupted upload history discards the saved progress.

## Undo the latest copy or move

The latest completed copy or move offers Undo in Tasks history for one hour. That deadline survives restarts. Transfers using Keep both are supported too.

A later accepted operation that changes user files clears the previous Undo set across all tabs. This includes delete, rename, uploads, editor saves, folder creation, permission changes and extraction. Failure or cancellation of the later operation does not restore the older set. Browsing, searching, previewing and downloading preserve it.

Copy Undo checks the copied files before removing them and reports conflicts if they have changed. Move Undo returns a folder with its current contents, including changes made afterward. Keep independent backups.

## Understand progress

Some tasks must count entries before a percentage is available. Delete first shows indeterminate progress, then completed/total items. Search results can be partial when a bound is reached. Cancellation is cooperative and may take time while an external tool or filesystem call returns.

Video thumbnail generation appears in Tasks while it is queued or running. Ordinary image thumbnails are not listed as tasks.
