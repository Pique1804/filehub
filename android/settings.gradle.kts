pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}
rootProject.name = "filehub-backup"
include(":app")

// Device tests are opt-in; normal releases never launch an emulator or require USB.
if (providers.gradleProperty("filehubBenchmarks").orNull == "true") include(":benchmark")
