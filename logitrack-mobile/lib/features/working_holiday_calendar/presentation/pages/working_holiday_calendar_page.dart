import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import '../../data/repositories/holiday_repository.dart';

class WorkingHolidayCalendarPage extends StatefulWidget {
  const WorkingHolidayCalendarPage({super.key});

  @override
  State<WorkingHolidayCalendarPage> createState() =>
      _WorkingHolidayCalendarPageState();
}

class _WorkingHolidayCalendarPageState extends State<WorkingHolidayCalendarPage> {
  final _repo = HolidayRepository();
  int _selectedYear = DateTime.now().year;

  @override
  Widget build(BuildContext context) {
    final isTh = context.locale.languageCode == 'th';

    return Scaffold(
      appBar: AppBar(
        title: Text('working_holiday_calendar_title'.tr()),
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          setState(() {});
        },
        child: CustomScrollView(
          physics: const AlwaysScrollableScrollPhysics(),
          slivers: [
            SliverToBoxAdapter(
              child: Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'working_holiday_calendar_subtitle'.tr(),
                      style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                            color: Theme.of(context)
                                .colorScheme
                                .onSurfaceVariant
                                .withOpacity(0.9),
                          ),
                    ),
                    const SizedBox(height: 16),
                    Row(
                      children: [
                        Text(
                          'working_holiday_calendar_year'.tr(),
                          style: Theme.of(context).textTheme.titleMedium,
                        ),
                        const SizedBox(width: 12),
                        DropdownButton<int>(
                          value: _selectedYear,
                          items: List.generate(5, (i) {
                            final y = DateTime.now().year - 2 + i;
                            return DropdownMenuItem(value: y, child: Text('$y'));
                          }),
                          onChanged: (v) {
                            if (v != null) setState(() => _selectedYear = v);
                          },
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            ),
            StreamBuilder<QuerySnapshot<Map<String, dynamic>>>(
              stream: _repo.watchHolidaysForYear(_selectedYear),
              builder: (context, snapshot) {
                if (snapshot.hasError) {
                  return SliverFillRemaining(
                    hasScrollBody: false,
                    child: Center(
                      child: Padding(
                        padding: const EdgeInsets.all(24),
                        child: Text(
                          'Error: ${snapshot.error}',
                          textAlign: TextAlign.center,
                        ),
                      ),
                    ),
                  );
                }
                if (!snapshot.hasData) {
                  return const SliverFillRemaining(
                    hasScrollBody: false,
                    child: Center(child: CircularProgressIndicator()),
                  );
                }
                final docs = snapshot.data!.docs;
                if (docs.isEmpty) {
                  return SliverFillRemaining(
                    hasScrollBody: false,
                    child: Center(
                      child: Padding(
                        padding: const EdgeInsets.all(24),
                        child: Column(
                          mainAxisAlignment: MainAxisAlignment.center,
                          children: [
                            Icon(
                              Icons.calendar_today_outlined,
                              size: 64,
                              color: Theme.of(context)
                                  .colorScheme
                                  .primary
                                  .withOpacity(0.5),
                            ),
                            const SizedBox(height: 16),
                            Text(
                              'working_holiday_calendar_no_holidays'.tr(),
                              textAlign: TextAlign.center,
                              style: Theme.of(context).textTheme.bodyLarge,
                            ),
                          ],
                        ),
                      ),
                    ),
                  );
                }
                return SliverPadding(
                  padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
                  sliver: SliverList(
                    delegate: SliverChildBuilderDelegate(
                      (context, index) {
                        final doc = docs[index];
                        final d = doc.data();
                        final date = (d['date'] as Timestamp?)?.toDate();
                        final type = d['type'] as String? ?? '';
                        final nameEn = d['holidayNameEN'] as String? ??
                            d['name'] as String? ??
                            '';
                        final nameTh = d['holidayNameTH'] as String? ?? '';
                        final name = isTh && nameTh.isNotEmpty
                            ? nameTh
                            : (nameEn.isNotEmpty ? nameEn : type);
                        final dateStr = date != null
                            ? DateFormat('dd MMM yyyy').format(date)
                            : '';

                        return Card(
                          margin: const EdgeInsets.only(bottom: 8),
                          child: ListTile(
                            leading: CircleAvatar(
                              backgroundColor: Theme.of(context)
                                  .colorScheme
                                  .primaryContainer,
                              child: Icon(
                                Icons.event,
                                color: Theme.of(context).colorScheme.primary,
                              ),
                            ),
                            title: Text(
                              name.isNotEmpty ? name : '—',
                              style:
                                  const TextStyle(fontWeight: FontWeight.w600),
                            ),
                            subtitle: dateStr.isNotEmpty
                                ? Text(dateStr)
                                : null,
                          ),
                        );
                      },
                      childCount: docs.length,
                    ),
                  ),
                );
              },
            ),
          ],
        ),
      ),
    );
  }
}
