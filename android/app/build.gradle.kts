plugins {
    id("androidx.baselineprofile")
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

val publisherKey = providers.environmentVariable("FILEHUB_SIGNING_KEYSTORE")
val unsignedBuild = providers.gradleProperty("filehubUnsigned").map { it.toBoolean() }.orElse(false)

android {
    namespace = "com.filehub.backup"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.filehub.backup"
        minSdk = 26
        targetSdk = 34
        versionCode = 72
        versionName = "1.40.22"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        vectorDrawables { useSupportLibrary = true }
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    signingConfigs {
        if (!unsignedBuild.get() && publisherKey.isPresent) {
            create("release") {
                storeFile = file(publisherKey.get())
                storePassword = providers.environmentVariable("FILEHUB_SIGNING_STORE_PASSWORD").get()
                keyAlias = providers.environmentVariable("FILEHUB_SIGNING_KEY_ALIAS").get()
                keyPassword = providers.environmentVariable("FILEHUB_SIGNING_KEY_PASSWORD").get()
            }
        }
    }
    buildTypes {
        release {
            // debuggable=false (implicit for release) drops Compose's debug instrumentation —
            // the big scroll-jank difference vs the assembleDebug builds shipped so far.
            isMinifyEnabled = true
            signingConfig = signingConfigs.findByName("release")
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }
    lint {
        checkReleaseBuilds = true
        abortOnError = true
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
    packaging {
        resources.merges += setOf("/META-INF/AL2.0", "/META-INF/LGPL2.1")
    }
}

dependencies {
    constraints {
        implementation("com.google.guava:guava:33.7.2-android") {
            because("Contains the fix for GHSA-xxph-c9ww-hj94")
        }
    }
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.robolectric:robolectric:4.13")
    testImplementation("com.squareup.okhttp3:mockwebserver:4.12.0")
    testImplementation("org.json:json:20240303")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.8.1")
    androidTestImplementation("androidx.test:runner:1.5.2")
    androidTestImplementation("androidx.test.ext:junit:1.1.5")
    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.activity:activity-compose:1.9.0")
    implementation(platform("androidx.compose:compose-bom:2024.06.00"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.4")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.4")
    implementation("androidx.work:work-runtime-ktx:2.9.1")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.8.4")
    implementation("androidx.paging:paging-runtime:3.3.6")
    implementation("androidx.paging:paging-compose:3.3.6")
    implementation("androidx.profileinstaller:profileinstaller:1.4.1")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("io.coil-kt:coil-compose:2.6.0")
    implementation("net.engawapg.lib:zoomable:1.6.2")          // zoomable full-screen image viewer
    implementation("androidx.biometric:biometric:1.1.0")       // app-lock (fingerprint/face/PIN)
    implementation("androidx.fragment:fragment-ktx:1.8.1")     // FragmentActivity for BiometricPrompt
    implementation("org.maplibre.gl:android-sdk:12.0.0")       // configurable basemap and screen projection
    implementation("androidx.media3:media3-exoplayer:1.3.1")           // video playback in the viewer
    implementation("androidx.media3:media3-exoplayer-hls:1.3.1")
    implementation("androidx.media3:media3-ui:1.3.1")
    implementation("androidx.media3:media3-datasource-okhttp:1.3.1")   // authed streaming via Net.client
}

baselineProfile {
    automaticGenerationDuringBuild = false
    saveInSrc = true
}
if (providers.gradleProperty("filehubBenchmarks").orNull == "true") {
    dependencies { baselineProfile(project(":benchmark")) }
}
// The bounded Windows runner includes this test worker in its aggregate memory cap.
tasks.withType<Test>().configureEach { maxHeapSize = "768m"; maxParallelForks = 1 }
