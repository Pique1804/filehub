package com.filehub.benchmark

import androidx.benchmark.macro.*
import androidx.benchmark.macro.junit4.BaselineProfileRule
import androidx.benchmark.macro.junit4.MacrobenchmarkRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.uiautomator.By
import androidx.test.uiautomator.Until
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

private const val APP="com.filehub.backup"
private fun MacrobenchmarkScope.waitForGallery() {
    check(device.wait(Until.hasObject(By.text("Фотографии")),10_000)) {
        "Use a dedicated test installation with saved synthetic account and app lock disabled."
    }
}
private fun MacrobenchmarkScope.tabsAndScroll() {
    waitForGallery()
    for(label in listOf("Файлы","Настройки","Фото")) {
        val tab=device.findObject(By.text(label)) ?: error("Missing tab: $label")
        tab.click();device.waitForIdle()
    }
    val x=device.displayWidth/2
    repeat(4){device.swipe(x,device.displayHeight*3/4,x,device.displayHeight/3,20);device.waitForIdle()}
}
@RunWith(AndroidJUnit4::class)
class BaselineProfileGenerator {
    @get:Rule val rule=BaselineProfileRule()
    @Test fun startup()=rule.collect(APP,includeInStartupProfile=true){pressHome();startActivityAndWait();waitForGallery()}
    @Test fun navigation()=rule.collect(APP,includeInStartupProfile=false){startActivityAndWait();tabsAndScroll()}
}
@RunWith(AndroidJUnit4::class)
class StartupBenchmark {
    @get:Rule val rule=MacrobenchmarkRule()
    @Test fun coldStartup()=rule.measureRepeated(packageName=APP,metrics=listOf(StartupTimingMetric()),iterations=10,startupMode=StartupMode.COLD,
        compilationMode=CompilationMode.Partial(baselineProfileMode=BaselineProfileMode.UseIfAvailable),setupBlock={pressHome()}) {
        startActivityAndWait();waitForGallery()
    }
    @Test fun tabReturnAndGalleryFrames()=rule.measureRepeated(packageName=APP,metrics=listOf(FrameTimingMetric()),iterations=5,
        compilationMode=CompilationMode.Partial(baselineProfileMode=BaselineProfileMode.UseIfAvailable),setupBlock={startActivityAndWait()}) {tabsAndScroll()}
}
