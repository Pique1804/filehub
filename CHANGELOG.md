# Changelog

## 1.40.22

- Revoke album links in the web gallery and Android. Share albums again if
  their links were created by an earlier release.
- Android background photo backup requires a trusted HTTPS connection. Manual
  file access and uploads remain available over trusted home HTTP.
- FileHub preserves a consistent SQLite snapshot before restoring the catalogue
  and asks for recovery if a configured installation loses its database.
- The Linux installer checks storage identity and supports upgrading official
  1.40.20 and 1.40.21 installations. The archive includes the English setup guide,
  FAQ and Android screenshot gallery.

## 1.40.21 · installer revision 1

- Open the private setup link printed at the end of installation.
- Select multiple disks with checkboxes, expand their folder trees and see
  when more folders are available by scrolling.
- Choose an existing or empty photo folder, or finish setup without photos.
- Add disks and photo folders later from the storage settings.
- Configure the optional photo search service for your own host and network.

## 1.40.21

- Refresh Android photo libraries with more than 50,000 items. Large refreshes preserve the last complete cache until every page arrives.
- Offline downloads wait for network access and automatically retry temporary failures. A retry starts the download from the beginning.

## 1.40.20

- Install the Linux server, embedded web app and signed Android APK from one verified archive.
- Upgrade an official Linux 1.40.1 installation with a disk-space check and a private SQLite recovery snapshot.
- Build a Compose image with optional LibreOffice previews and libvips image processing.
- Browse web photos with adjustable tile density. Search by photo content when local indexing is enabled.
- Restore or permanently delete selected trash items, sort lists by headers, and move or copy files by dragging or using keyboard shortcuts.
- See file owners, backup status and storage quota information where supported.

## Android 1.40.14

- A Downloads screen in Files and Videos shows the offline download queue, bytes received and failures.
- Offline saves continue when leaving the player. You can cancel a transfer or retry it from Downloads.
- Open saved videos in the native player without contacting the server. Saved documents can open in a compatible app on the device.

## Android 1.40.13

- Movie and series artwork, descriptions and manual TMDB matching in the video catalog.
- Administrator-only TMDB configuration in native Settings.
- Touch-friendly photo selection, move to a folder, add to an album and confirmed trash actions.

## Web and server updates

- User invitations, folder permissions and personal upload destinations enforced across clients.
- File lists and tiles with previews and menus in folders, recent files and search.
- Trigram filename search and type, extension, date, size and storage filters.
- Photo context menus and multi-selection, with places inside Photos.
- Movie and series library with viewing progress, local artwork and optional TMDB lookups.
- Dedicated settings page, compact storage shortcuts and document previews.

## Android 1.40.8

- Size the immersive playback window to the whole display so the app's tabs
  and background cannot show around its edges.

## 1.40.7

- Use the entire screen for video. Controls appear over the picture and hide
  automatically. Double tap either side to skip, or pick an episode from its
  season without leaving the player.
- Set playback speed and picture sizing. Android exposes audio and subtitle
  selection when the player can read those tracks.

## 1.40.6

- Use a dedicated video player in the web and Android apps. Browse episodes,
  switch to the next episode, seek through AVI and resume across devices.
- Use local posters and NFO descriptions, with video frames as artwork when
  no poster exists. Server operators can opt in to TVmaze series lookups by
  setting `FILEHUB_TVMAZE=1` in the server environment.

## 1.40.5

- Browse movies and series in the Videos section on web and Android. Choose
  existing folders for the library and continue watching across devices.
- Open AVI and other legacy videos through the compatible stream when FFmpeg
  is available on the server.

## 1.40.4

- Switch the web and Android interface among English, Russian and the system
  language. An Android device check confirmed that a running backup continues
  through a language change.
- Configure scheduled server backups from the web settings, including the folders,
  destination and schedule. Backups are opt-in.
- Review verified Android backup files before cleanup and select the files to
  remove. The system confirmation preserves all originals on cancel and
  removes only selected files on confirmation.
- Install Filehub with Docker Compose using ordinary host folders.
- Filter file search by storage, type, size and modification date.
- Reuse a verified upload of the same file to the same folder. The web also
  explains how to resume after reloading, and administrators can cancel incomplete
  transfers.
- Upload folder trees, choose a destination storage, and retry the failed items
  from a batch.
- Receive files through upload-only links with a size limit and expiration.
- Review the largest folders, storage usage categories and exact file duplicates.
- Keep files uploaded by Windows Explorer when WebDAV cannot change optional
  Windows file properties.

## Android 1.40.3

- Retain file and photo tabs and page the local photo timeline.
- Refresh cached screens after backup and photo permission changes.
- Fix preview memory use and cache closing on Android 8 and 9.

[Compatibility and verification](docs/release-verification-android-1.40.3.md)

## Server 1.40.2 and the media update

- Browse geotagged photos on the Places map in the web and Android clients.
- Browse large photo timelines in pages and zoom photos in the web viewer.
- Search for text recognized in photos and manage pausable web uploads.

[Usage](docs/maps-and-media.md) · [Verification](docs/release-verification-1.40.2.md)

## 1.40.1

- Install from ready-made Linux packages and configure storage from the setup wizard.
- Use the optional Proxmox storage adapter to attach existing filesystems.
- Apply the account, original-file and backup-receipt fixes included in this preview.

[Installation](docs/installation.md) · [Verification](docs/release-verification-1.40.1.md)
