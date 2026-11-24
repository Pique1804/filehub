/** FileHub's first-run, storage and phone-connection labels. */
const onboardingMessages = {
    interfaceLanguage: ['Язык интерфейса', 'Interface language'],
    systemLanguage: ['Как в системе', 'System language'],
    russianLanguage: ['Русский', 'Русский'],
    englishLanguage: ['English', 'English'],
    setupTitle: ["Настройка · Filehub", "Setup · Filehub"],
    homeSpace: ["Личное пространство", "Personal space"],
    setupStages: ["Этапы настройки", "Setup steps"],
    account: ["Аккаунт", "Account"],
    storage: ["Хранилище", "Storage"],
    photos: ["Фотографии", "Photos"],
    finishStep: ["Завершение", "Finish"],
    firstRun: ["Первоначальная настройка", "Initial setup"],
    setupHeading: ["Настройка Filehub", "Set up Filehub"],
    checkingStorage: ["Проверяем хранилище…", "Checking storage…"],
    createAccount: ["Создайте аккаунт", "Create your account"],
    accountHint: ["Это аккаунт администратора. С ним вы сможете войти в веб-панель и приложение.", "This administrator account lets you sign in to the web panel and the app."],
    setupCode: ["Код настройки", "Setup code"],
    setupCodeHint: ["Код из терминала, где устанавливали Filehub. Он подтверждает, что сервер ваш; пароль аккаунта вы задаёте ниже.", "Enter the code shown in the terminal during Filehub installation. Choose your account password below."],
    username: ["Логин", "Username"],
    password: ["Пароль", "Password"],
    passwordHint: ["От 12 символов. Подойдёт парольная фраза.", "Use at least 12 characters. A passphrase works well."],
    repeatPassword: ["Повторите пароль", "Confirm password"],
    next: ["Продолжить", "Continue"],
    storageQuestion: ["Где хранить файлы?", "Where should Filehub store files?"],
    storageHint: ["Начните с диска сервера или подключите отдельный HDD, SSD или USB-диск.", "Start with a folder on this server or attach a separate HDD, SSD, or USB drive."],
    storageChoice: ["Место хранения", "Storage location"],
    useServer: ["На этом сервере", "On this server"],
    serverFolder: ["Папка на системном диске", "Folder on the system drive"],
    noExtraDisk: ["Можно пользоваться сразу, без отдельного HDD", "Ready to use with the server drive"],
    separateDisk: ["Отдельный диск", "Separate drive"],
    diskTypes: ["HDD, SSD или USB", "HDD, SSD, or USB drive"],
    diskHint: ["Выберите один или несколько дисков или подключённых папок", "Choose one or more drives or mounted folders"],
    attachDrive: ["Подключение диска", "Connect a drive"],
    mountedFolders: ["Подключённые папки", "Mounted folders"],
    useMountedFolder: ["Выбрать папку", "Choose folder"],
    shownInFilehub: ["Будет видно в Filehub", "Visible in Filehub"],
    back: ["Назад", "Back"],
    photoQuestion: ["Откуда брать фотографии?", "Where should photos come from?"],
    photoHint: ["Filehub покажет в фотоленте снимки из выбранных папок. Оригиналы останутся на своих местах и будут доступны в разделе «Файлы».", "Filehub shows photos from the selected folders. The originals stay where they are and remain accessible in Files."],
    photoFirstRun: ["Выберите папку с готовой коллекцией или пустую папку для новых снимков. Настроить фотоленту можно и после запуска.", "Choose a folder with existing photos or an empty folder for new pictures. You can also set up the photo library after launch."],
    skipPhotos: ["Пропустить фото", "Skip photos"],
    photosNotConfigured: ["Настроить позже", "Set up later"],
    photosLaterHint: ["Добавьте папку для фотографий в настройках хранилищ, когда она понадобится.", "Add a photo folder in storage settings when you need one."],
    configurePhotos: ["Настроить фотографии", "Set up photos"],
    reviewSettings: ["Проверьте настройки", "Review your settings"],
    afterSetup: ["После запуска можно скачать приложение и подключить телефон. Хранилища и папки фото можно добавить позже в настройках.", "After setup, download the app and connect your phone. You can add storage and photo folders later in Settings."],
    startFilehub: ["Запустить Filehub", "Start Filehub"],
    phoneTitle: ["Хранилище и телефон · Filehub", "Storage and phone · Filehub"],
    phoneHeading: ["Хранилище и телефон", "Storage and phone"],
    phoneHint: ["Здесь видно, где лежат ваши файлы и как добавить фотографии.", "See where your files are stored and how to add photos."],
    openFilehub: ["← Открыть Filehub", "← Open Filehub"],
    storages: ["Хранилища", "Storage"],
    signInTitle: ["Войдите в свой Filehub", "Sign in to Filehub"],
    signIn: ["Войти", "Sign in"],
    yourStorage: ["Ваше хранилище", "Your storage"],
    libraryScope: ["Фотолента показывает фото из выбранной папки и её подпапок. Личные фото не добавляются сами: они появятся после вашей загрузки или из папки, выбранной при настройке.", "The photo library shows pictures from the selected folder and its subfolders. Add photos by uploading them or selecting an existing folder during setup."],
    computer: ["С КОМПЬЮТЕРА", "ON A COMPUTER"],
    firstPhotos: ["Добавьте первые фотографии", "Add your first photos"],
    openPhotoSource: ["Откройте папку", "Open the folder"],
    addPhotosInstruction: ["в Filehub и перетащите в неё фотографии или нажмите кнопку загрузки. Файлы сохранятся в выбранном вами хранилище.", "in Filehub, then drag photos into it or use the upload button. The files go to your selected storage."],
    photoSource: ["источник фото", "photo source"],
    openPhotoFolder: ["Открыть папку для фото", "Open the photo folder"],
    onAndroid: ["С ТЕЛЕФОНА · ANDROID", "ON YOUR ANDROID PHONE"],
    installApp: ["1. Установите приложение", "1. Install the app"],
    qrInstruction: ["Откройте эту страницу на телефоне: на компьютере можно отсканировать QR-код камерой телефона.", "Open this page on your phone. From a computer, scan the QR code with your phone's camera."],
    qrAlt: ["QR-код этой страницы для телефона", "QR code for opening this page on your phone"],
    downloadApp: ["Скачать Filehub для Android", "Download Filehub for Android"],
    apkInstructions: ["После скачивания откройте APK и подтвердите установку в Android.", "After downloading, open the APK and confirm installation in Android."],
    connectServer: ["2. Подключите свой сервер", "2. Connect to your server"],
    serverAddressHint: ["Откройте приложение. На экране входа замените адрес сервера на этот:", "Open the app. Use this server address on the sign-in screen."],
    serverAddress: ["Адрес сервера", "Server address"],
    copy: ["Скопировать", "Copy"],
    enterUsername: ["Введите логин", "Enter the username"],
    enterPassword: ["и пароль, который вы задали при настройке. Телефон должен быть в той же домашней сети.", "and the password you chose during setup. Your phone should be on the same local network."],
    selectUploadPhotos: ["3. Выберите фотографии для загрузки", "3. Choose photos to back up"],
    manualUploadTitle: ["3. Загрузите фотографии вручную", "3. Upload photos manually"],
    manualUploadHint: ["Откройте папку для фото в разделе «Файлы» приложения и нажмите «Загрузить». Автозагрузка в фоне доступна при подключении по HTTPS с сертификатом, которому доверяет телефон.", "Open your photo folder in Files and tap Upload. Automatic background uploads require HTTPS with a certificate trusted by your phone."],
    httpsInstructions: ["Настроить HTTPS", "Set up HTTPS"],
    uploadStepOne: ["В приложении откройте настройки и включите автозагрузку. Разрешите доступ к фотографиям.", "In the app settings, enable automatic backup and allow access to photos."],
    uploadStepTwo: ["Откройте настройки папки назначения и выберите хранилище и папку.", "Open the backup destination settings and choose the storage and folder shown below."],
    folder: ["Папка", "Folder"],
    phoneFolder: ["Папка внутри фотобиблиотеки", "Folder inside the photo library"],
    selectAlbums: ["Выберите альбомы телефона и при необходимости включите загрузку только по Wi-Fi. Затем запустите синхронизацию.", "Choose phone albums, enable Wi-Fi-only backup if needed, then start syncing."],
    goFiles: ["Перейти к файлам →", "Go to files →"],
    storageTitle: ["Хранилища · Filehub", "Storage · Filehub"],
    storageSettingsHint: ["Диски, папки для файлов и фотографии.", "Drives, file folders, and photos."],
    backFiles: ["← Файлы", "← Files"],
    connectPhone: ["Подключить телефон", "Connect your phone"],
    adminLogin: ["Вход администратора", "Administrator sign-in"],
    storageOfflineHint: ["Настройки доступны и при отключённом диске.", "Settings remain available when a drive is disconnected."],
    serverDevices: ["Устройства сервера", "Server drives"],
    serverDevicesHint: ["Подключите HDD, SSD или USB-диск к серверу, затем обновите список. Нужна готовая файловая система.", "Connect an HDD, SSD, or USB drive to the server, then refresh the list. The drive needs an existing filesystem."],
    refreshDevices: ["Обновить список", "Refresh list"],
    yourStorages: ["Ваши хранилища", "Your storage folders"],
    existingStorages: ["Эти диски и папки видны в разделе «Файлы». Выберите подключённое хранилище и нажмите «Добавить диск или папку».", "These drives and folders appear in Files. Select a connected location and choose “Add drive or folder”."],
    storageNames: ["Имена существующих хранилищ закреплены: их используют телефон и альбомы. Отключённый диск остаётся в списке, чтобы подключить его снова.", "Storage names already in use are fixed because the phone and albums reference them. A disconnected drive stays listed so you can reconnect it."],
    photoLibrary: ["Фотобиблиотека", "Photo library"],
    libraryFiles: ["Лента показывает оригиналы из этих папок. Копии файлов не создаются.", "The library shows the originals in these folders directly."],
    applySettings: ["Применить настройки", "Apply settings"],
    restartHint: ["Filehub ненадолго перезапустится. Текущая передача прервётся; приложение с докачкой сможет продолжить её после запуска. Файлы и учётные записи сохранятся.", "Filehub restarts briefly. Any current transfer pauses, and clients that support resuming can continue it afterward. Your files and accounts are preserved."],
    saveRestart: ["Сохранить и перезапустить Filehub", "Save and restart Filehub"],
    requestFailed: ["Не удалось выполнить запрос.", "The request failed."],
    stepSaved: ["Шаг и папки сохранены", "Step and folders saved"],
    savingStep: ["Сохраняем шаг…", "Saving your progress…"],
    stepNotSaved: ["Шаг пока не сохранён", "Progress is still unsaved"],
    stepCounter: ["Шаг {current} из 4", "Step {current} of 4"],
    allContents: ["всё содержимое", "all contents"],
    inFiles: ["В «Файлах»", "In Files"],
    inPhotos: ["В «Фото»", "In Photos"],
    continueSetup: ["Продолжите настройку", "Continue setup"],
    continueHint: ["Войдите в созданный аккаунт. Сохранённый шаг и папки восстановятся.", "Sign in with the account you created to restore your step and folders."],
    signInToContinue: ["Войдите, чтобы продолжить с сохранённого шага.", "Sign in to continue from your saved step."],
    stepRestored: ["Продолжили с сохранённого шага", "Restored your saved step"],
    selectedDriveOffline: ["Выбранный диск сейчас недоступен. Папки сохранены; подключите прежний диск.", "The selected drive is unavailable. Reconnect it to access your saved folders."],
    confirmExistingPhotos: ["Подтвердите существующие фотографии перед запуском.", "Confirm that you want to show existing photos before starting Filehub."],
    files: ["Файлы", "Files"],
    passwordMax: ["Пароль должен занимать не больше 72 байт.", "The password must be at most 72 bytes."],
    passwordMismatch: ["Пароли не совпадают.", "The passwords do not match."],
    selectDrive: ["Подключите и выберите диск или нажмите «На этом сервере».", "Connect and select a drive, or choose “On this server”."],
    selectLocation: ["Выберите место для файлов.", "Choose where to store your files."],
    chooseExistingPhotos: ["Выберите папку через «Выбрать папку с фото», отметьте показ существующих снимков или нажмите «Пропустить фото».", "Choose a photo folder, confirm showing existing photos, or select Skip photos."],
    confirmExistingFolder: ["Подтвердите добавление фотографий из существующей папки.", "Confirm that you want to show photos from the existing folder."],
    savedOpenStorage: ["Настройки сохранены. Откройте настройки хранилищ.", "Settings saved. Open storage settings."],
    serverFolderOffline: ["Папка сервера недоступна. Проверьте подключение хранилища.", "The server folder is unavailable. Check the storage connection."],
    freeSpace: ["Свободно {size}", "{size} free"],
    driveNotInContainer: ["Диск ещё не появился в контейнере. Нажмите «Проверить подключение».", "The drive has not appeared in the container yet. Select “Check connection”."],
    getSettingsFailed: ["Не удалось получить настройки.", "Could not load settings."],
    storageRoot: ["корень хранилища", "storage root"],
    readyHeading: ["Ваш Filehub готов", "Filehub is ready"],
    signInFailed: ["Не удалось войти. Проверьте логин и пароль.", "Could not sign in. Check your username and password."],
    addressCopied: ["Адрес скопирован", "Address copied"],
    addressSelected: ["Адрес выделен. Скопируйте его и вставьте в приложение.", "Copy the selected address and paste it into the app."],
    serverRunning: ["Filehub работает.", "Filehub is running."],
    serverStopped: ["Файловый сервер остановлен. Настройки сохранены.", "The file server stopped. Your settings are saved."],
    driveNotReady: ["Диск ещё не появился. Проверьте подключение.", "The drive is unavailable. Check the connection."],
    confirmStoragePhotos: ["Подтвердите добавление существующих фотографий из выбранной папки.", "Confirm that you want to show existing photos from the selected folder."],
    confirmRestart: ["Сохранить настройки и перезапустить Filehub? Активные передачи временно прервутся.", "Save settings and restart Filehub? Active transfers will pause."],
    savedServerFailed: ["Настройки сохранены, но файловый сервер не запустился.", "Settings saved. The file server could not start."],
    driveDisconnected: [" · отключено", " · disconnected"],
    folderPrefix: ["Папка: ", "Folder: "],
    diskRoot: ["Корень диска", "Drive root"],
    availableFolder: ["Вся доступная папка", "Entire available folder"],
    freeOfTotal: ["Свободно {free} из {total}", "{free} free of {total}"],
    scopeLabel: ["Что показывать в «Файлах»", "Show in Files"],
    wholeDisk: ["Весь диск", "Entire drive"],
    onlySelectedFolder: ["Только выбранную папку", "Only the selected folder"],
    fileFolder: ["Папка для файлов", "File folder"],
    browseFolder: ["Выбрать папку", "Choose folder"],
    readOnlyRoot: ["Корень диска доступен для просмотра. Новые файлы можно загружать в доступные для записи папки внутри диска. Права на существующие папки не изменяются.", "You can browse the drive root and upload files to writable folders on this drive. Existing folder permissions are preserved."],
    displayName: ["Имя в Filehub", "Display name in Filehub"],
    sourceName: ["Имя хранилища в Filehub", "Storage name in Filehub"],
    sourceNameHint: ["Латинские буквы, цифры, дефис или подчёркивание. Это имя появится в разделе «Файлы».", "Use Latin letters, digits, a hyphen, or an underscore. This name appears in Files."],
    removeFromList: ["Убрать из списка", "Remove from the list"],
    newFolderStorage: ["Диск или папка", "Drive or folder"],
    driveUnavailable: [" · недоступно", " · unavailable"],
    addDiskOrFolder: ["Добавить диск или папку", "Add drive or folder"],
    storageLimit: ["Можно добавить не больше 16 хранилищ.", "You can add up to 16 storage locations."],
    noFolderAvailable: ["Нет доступной папки.", "There are no available folders."],
    photoStorage: ["Хранилище с фотографиями", "Photo storage"],
    includeExisting: ["Добавить существующие фотографии", "Include existing photos"],
    photosFolder: ["Папка с фотографиями", "Photo folder"],
    pickPhotosFolder: ["Выбрать папку с фото", "Choose photo folder"],
    selectSource: ["Выберите источник.", "Choose a storage source."],
    confirmPhotosHint: ["В этой папке уже есть файлы. Отметьте пункт ниже, чтобы показать существующие фотографии в фотоленте, или выберите другую папку.", "This folder already contains files. Select the option below to show existing photos in the library, or choose another folder."],
    includeExistingHint: ["Показать существующие фотографии из этой папки и её подпапок. Файлы останутся на месте.", "Show existing photos in this folder and its subfolders. Files stay in place."],
    removeFromPhotos: ["Убрать папку из фотоленты", "Remove folder from photo library"],
    addPhotoFolder: ["Добавить ещё папку с фото", "Add another photo folder"],
    selectStorageFirst: ["Сначала выберите хранилище.", "Select storage first."],
    photoFolderLimit: ["Допускается не больше 32 фото-папок.", "You can add up to 32 photo folders."],
    pickFolderHeading: ["Выберите папку", "Choose a folder"],
    pickThisFolder: ["Выбрать эту папку", "Choose this folder"],
    cancel: ["Отмена", "Cancel"],
    folderFailed: ["Не удалось открыть папку.", "Could not open the folder."],
    openFolderProgress: ["Открываем папку…", "Opening folder…"],
    selectedFolderContents: ["Содержимое выбранной папки", "Contents of the selected folder"],
    folderCounts: ["Подпапок: {folders}. Фото и видео в этой папке: {media}.", "Subfolders: {folders}. Photos and videos here: {media}."],
    folderListLimit: ["Список ограничен первыми 2000 записями.", "The list shows the first 2,000 entries."],
    folderExpand: ["Подпапки можно раскрыть в дереве.", "Expand a folder to see its contents."],
    emptyFolder: ["Папка пустая.", "This folder is empty."],
    allFolders: ["Все папки", "All folders"],
    folderTreeLabel: ["Список папок", "Folder list"],
    foldersBelow: ["↓ Ещё папки ниже", "↓ More folders below"],
    foldersAbove: ["↑ Ещё папки выше", "↑ More folders above"],
    sourceNameInvalid: ["В имени источника допустимы латинские буквы, цифры, - и _.", "Use Latin letters, digits, - or _ in the source name."],
    checkingHost: ["Загружаем список дисков…", "Loading drives…"],
    noDrive: ["Пока нет доступных дисков. Подключите HDD, SSD или USB-диск к серверу и обновите список.", "Connect a drive to the server and refresh the list."],
    driveMounted: ["Смонтирован и доступен Filehub", "Mounted and available to Filehub"],
    driveReady: ["Готов к подключению", "Ready to connect"],
    testImage: ["Проверочный образ диска. Это не физический HDD.", "Test disk image."],
    useDrive: ["Использовать этот диск", "Use this drive"],
    mountDrive: ["Подключить и использовать", "Connect and use"],
    selectingDrive: ["Выбираем диск…", "Selecting drive…"],
    mountingDrive: ["Монтируем диск…", "Mounting drive…"],
    driveSelected: ["Диск выбран.", "Drive selected."],
    selected: ["Выбран", "Selected"],
    proxmoxConnectDrive: ["Как подключить HDD или SSD в Proxmox", "How to connect a drive in Proxmox"],
    proxmoxOpenShell: ["Подключите диск к серверу. В панели Proxmox в дереве слева выберите сам сервер и откройте Shell.", "Plug the drive into the server. In the Proxmox sidebar, select the server itself and open Shell."],
    proxmoxRunInstaller: ["В Shell узла запустите эту команду из папки с распакованным установщиком FileHub.", "In the node shell, run this command from the folder containing the extracted FileHub installer."],
    proxmoxContainerNumber: ["Замените CTID на номер контейнера FileHub из дерева Proxmox. Контейнер перезапустится, шаги мастера сохранятся.", "Replace CTID with the number next to the FileHub container in Proxmox. The container will restart and setup progress will be saved."],
    proxmoxPickDrive: ["Вернитесь сюда, нажмите «Обновить список» и выберите на нужном диске «Подключить и использовать». FileHub смонтирует раздел с готовой файловой системой.", "Return here, select Refresh list, then choose Connect and use for your drive. FileHub will mount the partition using its existing filesystem."],
    mountedFolderProxmox: ["Если папка со смонтированного диска уже была передана контейнеру при установке, выберите её в разделе «Подключённые папки».", "If the mounted drive's folder was passed into the container at installation, select it under Mounted folders."],
    mountedFolderContainer: ["Чтобы использовать диск, передайте его папку в контейнер при установке FileHub.", "To use a drive, pass its folder into the container when installing FileHub."],
    mountedFolderServer: ["Выберите папку диска, смонтированного при установке FileHub.", "Choose a folder on a drive that was mounted when FileHub was installed."],
    diskServiceUnavailable: ["Сервис дисков не отвечает. Проверьте его на сервере и нажмите «Обновить список».", "The disk service is unavailable. Check it on the server, then select Refresh list."],
};

const onboardingServerMessages = new Map([
    ['Неверный логин или пароль.', 'Incorrect username or password.'],
    ['Слишком много попыток. Повторите вход через 15 минут.', 'Too many attempts. Try signing in again in 15 minutes.'],
    ['Введите код настройки из терминала установки. Это не пароль аккаунта.', 'Enter the setup code from the installation terminal.'],
    ['Логин: от 1 до 32 латинских букв, цифр, точек, дефисов или подчёркиваний.', 'Use 1 to 32 Latin letters, digits, periods, hyphens, or underscores in the username.'],
    ['Пароль должен содержать от 12 до 72 байт.', 'The password must be between 12 and 72 bytes.'],
    ['Первоначальная настройка уже завершена. Войдите в Filehub.', 'Initial setup is complete. Sign in to Filehub.'],
    ['Настройка уже завершена.', 'Setup is already complete.'],
    ['Войдите в Filehub.', 'Sign in to Filehub.'],
    ['Войдите в Filehub, чтобы открыть настройки.', 'Sign in to Filehub to open settings.'],
    ['Обновите страницу настройки и повторите.', 'Refresh the setup page and try again.'],
    ['Недопустимый адрес сервера.', 'Invalid server address.'],
    ['Хранилище недоступно. Подключите прежний диск.', 'Storage is unavailable. Reconnect the original drive.'],
    ['Выбранный путь содержит ссылку, файл или недоступную папку.', 'The selected path contains a link, file, or inaccessible folder.'],
    ['Папка недоступна для записи.', 'The folder is not writable.'],
    ['Неверная папка.', 'Invalid folder.'],
    ['Выберите обычную папку внутри хранилища.', 'Choose a normal folder inside storage.'],
    ['Не удалось открыть хранилище. Проверьте подключение и права.', 'Could not open storage. Check the connection and permissions.'],
    ['Не удалось сохранить настройки. Проверьте свободное место и права хранилища.', 'Could not save settings. Check free space and storage permissions.'],
    ['Диски хоста ещё не подключены к Filehub.', 'Host drives are unavailable to Filehub.'],
    ['Связь с сервисом дисков потеряна. Проверьте подключение на хосте.', 'Connection to the drive service was lost. Check the host connection.'],
    ['Настройки хранилищ доступны администратору.', 'Storage settings require an administrator account.'],
    ['Настройки уже изменились. Обновите страницу перед сохранением.', 'Settings changed. Refresh the page before saving.'],
]);

const onboardingLanguageChoices = ['system', 'ru', 'en'];
const onboardingLanguageKey = 'fh_language';
let onboardingLanguage = 'system';
try {
    const saved = localStorage.getItem(onboardingLanguageKey);
    if (onboardingLanguageChoices.includes(saved)) onboardingLanguage = saved;
} catch { /* Language selection still works for the current page. */ }

function fhLocale() {
    if (onboardingLanguage !== 'system') return onboardingLanguage;
    const languages = navigator.languages?.length ? navigator.languages : [navigator.language];
    for (const value of languages) {
        const base = value?.toLowerCase().split('-')[0];
        if (base === 'ru' || base === 'en') return base;
    }
    return 'en';
}

function fhText(key, values = {}) {
    const message = onboardingMessages[key]?.[fhLocale() === 'ru' ? 0 : 1] ?? key;
    return message.replace(/\{(\w+)\}/g, (match, name) => Object.hasOwn(values, name) ? values[name] : match);
}

function fhServerText(message) {
    return fhLocale() === 'en' ? onboardingServerMessages.get(message) ?? message : message;
}

function fhRefreshNotice(node) {
    const current = node.textContent;
    const label = Object.entries(onboardingMessages).find(([, pair]) => pair.includes(current));
    if (label) {
        node.textContent = fhText(label[0]);
    } else if (fhLocale() === 'ru') {
        const error = [...onboardingServerMessages].find(([, english]) => english === current);
        if (error) node.textContent = error[0];
    } else {
        node.textContent = fhServerText(current);
    }
}

function fhNumber(value, options) {
    return new Intl.NumberFormat(fhLocale(), options).format(value);
}

function fhCapacity(bytes, unit = 'GiB') {
    const divisor = unit === 'MiB' ? 2**20 : 2**30;
    const localizedUnit = fhLocale() === 'ru' ? (unit === 'MiB' ? 'МиБ' : 'ГиБ') : unit;
    return fhNumber(bytes / divisor, {maximumFractionDigits: 1}) + '\u00a0' + localizedUnit;
}

function fhAssignText(node, key) {
    node.dataset.text = key;
    node.textContent = fhText(key);
}

function fhApplyLanguage() {
    document.documentElement.lang = fhLocale();
    for (const node of document.querySelectorAll('[data-text]')) {
        node.textContent = fhText(node.dataset.text);
    }
    for (const [attr, key] of [['aria-label', 'label'], ['alt', 'alt']]) {
        for (const node of document.querySelectorAll('[data-' + key + ']')) {
            node.setAttribute(attr, fhText(node.dataset[key]));
        }
    }
}

const onboardingLanguageChoice = document.querySelector('#language-choice');
if (onboardingLanguageChoice) {
    const select = document.createElement('select');
    select.dataset.label = 'interfaceLanguage';
    for (const [value, text] of [['system', 'systemLanguage'], ['ru', 'russianLanguage'], ['en', 'englishLanguage']]) {
        const option = document.createElement('option');
        option.value = value;
        option.dataset.text = text;
        select.append(option);
    }
    select.value = onboardingLanguage;
    select.addEventListener('change', () => {
        onboardingLanguage = select.value;
        try { localStorage.setItem(onboardingLanguageKey, onboardingLanguage); } catch { /* Current page only. */ }
        fhApplyLanguage();
        window.dispatchEvent(new Event('fh-language-change'));
    });
    onboardingLanguageChoice.append(select);
}
fhApplyLanguage();
