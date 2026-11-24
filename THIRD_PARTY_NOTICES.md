# Third-party notices

The root MIT license covers Filehub's own source code. It does not relicense
third-party components or the photographs shown in documentation.

| Component | License / notice |
| --- | --- |
| Golos Text | SIL Open Font License 1.1; [full notice](docs/licenses/Golos-Text-OFL.txt), also distributed beside the onboarding font |
| Material Symbols | Apache License 2.0; [full notice](server/frontend/public/licenses/Material-Symbols-LICENSE.txt) |
| Noble Hashes 1.8.0 | MIT; [full notice](docs/licenses/Noble-Hashes-LICENSE.txt), also distributed with the web client |
| Documentation photographs | NASA Image and Video Library; [individual credits and usage guidelines](docs/screenshot-credits.md) |

Go dependencies are declared in `server/go.mod` and `server/go.sum`. Web
components are declared in `server/frontend/package.json` and its lockfile.
Android components are declared in `android/app/build.gradle.kts`. Their respective
upstream licenses remain applicable. The Android release APK includes a runtime
dependency index and the corresponding license texts and notices under
`assets/licenses/`. That directory also contains the font license and the native
map SDK notices.

Screenshots are genuine captures of a separate Filehub instance populated with
sample media. They do not contain the owner's personal library. NASA does not
endorse this project.

Media and map components added in 1.40.2:

| Component | License / upstream |
| --- | --- |
| MapLibre Native Android 12.0.0 | BSD-2-Clause, [Android SDK and third-party notices](https://github.com/maplibre/maplibre-native/blob/android-v12.0.0/platform/android/LICENSE.md), also included in the APK under `assets/licenses/` |
| MapLibre GL JS 6.18.0 | BSD-3-Clause, [license](https://github.com/maplibre/maplibre-gl-js/blob/v6.18.0/LICENSE.txt) |
| TanStack Virtual | MIT, [license](https://github.com/TanStack/virtual/blob/main/LICENSE) |
| PhotoSwipe 5.4.4 | MIT, [license](https://github.com/dimsemenov/PhotoSwipe/blob/v5.4.4/LICENSE) |
| Uppy Core / Tus | MIT, [license](https://github.com/transloadit/uppy/blob/main/LICENSE) |
| OpenStreetMap basemap | © OpenStreetMap contributors; [copyright](https://www.openstreetmap.org/copyright), [tile usage policy](https://operations.osmfoundation.org/policies/tiles/) |
| The Movie Database | Optional metadata and posters fetched with a key supplied by the server administrator. [API terms and attribution guidance](https://developer.themoviedb.org/docs/faq) |
| TVmaze | Series metadata from TVmaze. [API and licensing](https://www.tvmaze.com/api#licensing) |

The default map is an interactive raster basemap. It does not download areas
for offline use. Map libraries do not include a commercial tile subscription.
Operators can configure another provider; its terms and attribution still apply.
Filehub cookies are used only for the Filehub API, never attached to map-provider
requests. Photo coordinates are not sent to a geocoding service.

Full license texts for the map/media components are bundled in `docs/licenses/`
and included in the ready installer archive.
