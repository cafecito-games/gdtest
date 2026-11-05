extends GDTest

## Demonstrates custom matcher definitions for domain values and collection predicates.


## Typed receipt produced when items are purchased.
class PurchaseReceipt:
	extends RefCounted

	## Name of the purchased item.
	var item: String
	## Number of units purchased.
	var quantity: int
	## Total charged in cents.
	var total_cents: int


	## Creates a receipt with item name, quantity, and total price.
	func _init(item_name: String, item_quantity: int, total_price_cents: int) -> void:
		item = item_name
		quantity = item_quantity
		total_cents = total_price_cents


## Matches a PurchaseReceipt against expected item name, quantity, and total charge.
class MatchesReceipt:
	extends GDTestMatcher

	var _expected_item: String
	var _expected_quantity: int
	var _expected_total_cents: int


	## Stores the expected receipt values to match against.
	func _init(expected_item: String, expected_quantity: int, expected_total_cents: int) -> void:
		_expected_item = expected_item
		_expected_quantity = expected_quantity
		_expected_total_cents = expected_total_cents


	## Identifies this matcher in failure diagnostics.
	func name() -> String:
		return "matches_receipt"


	## Evaluates a candidate receipt and attaches expected attributes on mismatch.
	func match(actual: Variant) -> GDTestMatchResult:
		if (
				typeof(actual) != TYPE_OBJECT
				or not is_instance_valid(actual)
				or not actual is PurchaseReceipt
		):
			return GDTestMatchResult.unmatched(
					"expected PurchaseReceipt, got %s" % type_string(typeof(actual))
			)
		var receipt: PurchaseReceipt = actual
		if receipt.item != _expected_item:
			return GDTestMatchResult.unmatched(
					"item mismatch: expected %s, got %s" % [_expected_item, receipt.item],
					_expected_item,
					true
			)
		if receipt.quantity != _expected_quantity:
			return GDTestMatchResult.unmatched(
					"quantity mismatch: expected %d, got %d" % [
						_expected_quantity,
						receipt.quantity,
					],
					_expected_quantity,
					true
			)
		if receipt.total_cents != _expected_total_cents:
			return GDTestMatchResult.unmatched(
					"total mismatch: expected %d, got %d" % [
						_expected_total_cents,
						receipt.total_cents,
					],
					_expected_total_cents,
					true
			)
		return GDTestMatchResult.matched()


## Matches an inventory report dictionary where every count meets a threshold.
class AllStockAbove:
	extends GDTestMatcher

	var _threshold: int


	## Stores the minimum threshold each stock count must exceed.
	func _init(threshold: int) -> void:
		_threshold = threshold


	## Identifies this matcher in failure diagnostics.
	func name() -> String:
		return "all_stock_above"


	## Evaluates all stock counts in the report against the threshold.
	func match(actual: Variant) -> GDTestMatchResult:
		if not actual is Dictionary:
			return GDTestMatchResult.unmatched(
					"expected Dictionary stock report, got %s" % type_string(typeof(actual))
			)
		if actual.is_empty():
			return GDTestMatchResult.unmatched(
					"stock report is empty; expected items above threshold %d" % _threshold,
					_threshold,
					true
			)
		for item_name: Variant in actual:
			var count_value: Variant = actual[item_name]
			if not count_value is int or int(count_value) <= _threshold:
				return GDTestMatchResult.unmatched(
						"item %s count %s does not exceed threshold %d" % [
							str(item_name),
							str(count_value),
							_threshold,
						],
						_threshold,
						true
				)
		return GDTestMatchResult.matched()


## Verifies a domain receipt matches the expected attributes.
func test_domain_receipt_matcher() -> void:
	var receipt: PurchaseReceipt = PurchaseReceipt.new("potion", 3, 150)
	expect_that(receipt, MatchesReceipt.new("potion", 3, 150))


## Verifies that all items in an asynchronous shop stock report exceed a count threshold.
func test_collection_stock_report_matcher() -> void:
	var inventory: Inventory = Inventory.new(3)
	var shop: Shop = Shop.new(inventory)
	shop.stock("arrows", 20)
	shop.stock("shields", 5)
	var report: Dictionary[String, int] = await shop.stock_report()
	expect_that(report, AllStockAbove.new(0))
