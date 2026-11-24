package com.filehub.backup

import androidx.paging.PagingSource
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test
import java.time.LocalDate
import java.time.ZoneId

class PhotoTimelineTest {
    private val zone=ZoneId.of("Europe/Moscow")
    private fun time(date:String)=LocalDate.parse(date).atStartOfDay(zone).toEpochSecond()
    @Test fun headersAndPhotoOffsetsRemainGlobalAcrossPages()=runTest {
        val dates=List(500){if(it<301)"2026-11-16" else "2026-10-01"}
        val index=PhotoTimeline.build(dates.asSequence().map(::time),false,zone,LocalDate.parse("2026-11-16"))
        val photos=dates.mapIndexed {i,d->Photo("$i","$i","Disk",time(d),false,"rev",123)}
        assertEquals(502,index.itemCount);assertEquals(302,index.sections[1].itemStart)
        val reads=mutableListOf<Pair<Int,Int>>()
        val source=TimelinePagingSource(index){offset,count->reads.add(offset to count);photos.drop(offset).take(count)}
        val loaded=mutableListOf<TimelineRow>();var key:Int?=0
        do {
            val page=source.load(PagingSource.LoadParams.Refresh(key,240,true)) as PagingSource.LoadResult.Page
            assertEquals(loaded.size,page.itemsBefore);loaded.addAll(page.data);key=page.nextKey
        }while(key!=null)
        assertEquals(502,loaded.size)
        assertEquals(photos,loaded.filterIsInstance<TimelineRow.Image>().map {it.photo})
        assertTrue(reads.all {it.second<=240})
        val jumped=source.load(PagingSource.LoadParams.Refresh(302,50,true)) as PagingSource.LoadResult.Page
        assertTrue(jumped.data.first() is TimelineRow.Header);assertEquals(302,jumped.itemsBefore)
        val prepended=source.load(PagingSource.LoadParams.Prepend(302,50,true)) as PagingSource.LoadResult.Page
        assertEquals(252,prepended.itemsBefore);assertEquals(302,prepended.nextKey)
        assertEquals(photos[251],(prepended.data.first() as TimelineRow.Image).photo)
    }
    @Test fun monthlyIndexAndEmptyLibrary() {
        val index=PhotoTimeline.build(sequenceOf(time("2026-11-16"),time("2026-11-01"),time("2026-10-31")),true,zone)
        assertEquals(listOf("2026-11","2026-10"),index.sections.map {it.key})
        assertEquals(2,index.sections.first().count);assertEquals(3,index.photoOffset(5))
        assertEquals(0,PhotoTimeline.build(emptySequence(),false).itemCount)
        assertNull(PhotoTimeline().headerAt(0))
    }
}
